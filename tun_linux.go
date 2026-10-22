//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const tunToolName = "tun2socks"

func tunPlatformSupported() bool { return true }

func tunPlatformCheck() error {
	if !isElevated() {
		return errors.New("TUN 模式需要 root 权限：请用 sudo 运行 tcp2rdp-web")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return errors.New("没有找到 ip 命令：请安装 iproute2")
	}
	if _, err := findTunTool(); err != nil {
		return err
	}
	return nil
}

// findTunTool looks where the build puts bundled binaries: next to the app, in
// resources/, or in bin/ during development.
func findTunTool() (string, error) {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, tunToolName),
			filepath.Join(dir, "resources", tunToolName),
			filepath.Join(dir, "bin", tunToolName),
			filepath.Join(dir, "..", tunToolName),
			filepath.Join(dir, "..", "bin", tunToolName),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, tunToolName),
			filepath.Join(wd, "bin", tunToolName),
			filepath.Join(wd, "..", tunToolName),
			filepath.Join(wd, "..", "bin", tunToolName),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(c); err == nil {
				return abs, nil
			}
			return c, nil
		}
	}
	if p, err := exec.LookPath(tunToolName); err == nil {
		return p, nil
	}
	return "", errors.New("没有找到 tun2socks：从 github.com/xjasonlyu/tun2socks 下载对应架构的二进制，重命名为 tun2socks 放到程序目录或 bin/")
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func runHidden(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

/* ---------------------------------------------------- interfaces & routes */

// linuxInterface reports the state of one interface: index, whether it is up,
// and whether it carries the TUN address.
func linuxInterface(name string) (ifIndex uint32, up bool, hasIP bool, err error) {
	out, err := runHidden("ip", "-o", "link", "show", "dev", name)
	if err != nil {
		return 0, false, false, err
	}
	first := strings.TrimSpace(strings.SplitN(out, ":", 2)[0])
	n, _ := strconv.Atoi(first)
	up = strings.Contains(out, "state UP")
	addrOut, _ := runHidden("ip", "-4", "-o", "addr", "show", "dev", name)
	hasIP = strings.Contains(addrOut, tunAddress)
	return uint32(n), up, hasIP, nil
}

type linuxRoute struct {
	prefix string // "default" or a CIDR ("10.0.0.0/8", "1.2.3.4")
	gw     string
	dev    string
}

func linuxRoutes() ([]linuxRoute, error) {
	out, err := runHidden("ip", "-4", "route", "show")
	if err != nil {
		return nil, fmt.Errorf("读取路由表失败：%v", err)
	}
	var routes []linuxRoute
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		r := linuxRoute{prefix: f[0]}
		for i := 1; i+1 < len(f); i++ {
			switch f[i] {
			case "via":
				r.gw = f[i+1]
			case "dev":
				r.dev = f[i+1]
			}
		}
		routes = append(routes, r)
	}
	return routes, nil
}

func parseRoutePrefix(prefix string) (*net.IPNet, bool) {
	if prefix == "default" {
		return &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}, true
	}
	if !strings.Contains(prefix, "/") {
		ip := net.ParseIP(prefix).To4()
		if ip == nil {
			return nil, false
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}, true
	}
	_, ipnet, err := net.ParseCIDR(prefix)
	if err != nil {
		return nil, false
	}
	if ipnet.IP.To4() == nil {
		return nil, false
	}
	return ipnet, true
}

// physicalDefaultRoute picks the default route that is not the TUN device. It
// is the path kept for the tunnel server and DNS.
func physicalDefaultRoute(routes []linuxRoute, tunDev string) (string, string, error) {
	for _, r := range routes {
		if r.prefix == "default" && r.dev != "" && r.dev != tunDev && r.gw != "" {
			return r.dev, r.gw, nil
		}
	}
	return "", "", errors.New("没有找到可用的物理默认网关")
}

// coveredByPhysical answers "does this address already leave through a real
// interface via a route more specific than the /1 split TUN installs?" A LAN
// server or an on-link DNS needs no extra /32.
func coveredByPhysical(routes []linuxRoute, ipStr, tunDev string) bool {
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return false
	}
	bestLen := -1
	bestDev := ""
	for _, r := range routes {
		ipnet, ok := parseRoutePrefix(r.prefix)
		if !ok {
			continue
		}
		plen, _ := ipnet.Mask.Size()
		if plen <= 1 {
			continue
		}
		if ip.Mask(ipnet.Mask).Equal(ipnet.IP.Mask(ipnet.Mask)) && plen > bestLen {
			bestLen = plen
			bestDev = r.dev
		}
	}
	return bestLen > 0 && bestDev != "" && bestDev != tunDev
}

// bestRouteDev follows longest-prefix matching, which is how the kernel decides
// where a destination really goes.
func bestRouteDev(routes []linuxRoute, ipStr string) (string, bool) {
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return "", false
	}
	bestLen := -1
	bestDev := ""
	for _, r := range routes {
		ipnet, ok := parseRoutePrefix(r.prefix)
		if !ok {
			continue
		}
		plen, _ := ipnet.Mask.Size()
		if ip.Mask(ipnet.Mask).Equal(ipnet.IP.Mask(ipnet.Mask)) && plen > bestLen {
			bestLen = plen
			bestDev = r.dev
		}
	}
	return bestDev, bestLen >= 0
}

func hasSplitRoutes(dev string) bool {
	routes, err := linuxRoutes()
	if err != nil {
		return false
	}
	count := 0
	for _, r := range routes {
		if r.dev == dev && (r.prefix == "0.0.0.0/1" || r.prefix == "128.0.0.0/1") {
			count++
		}
	}
	return count == 2
}

func addSplitRoutes(dev string) error {
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if _, err := runHidden("ip", "route", "replace", prefix, "dev", dev, "metric", "1"); err != nil {
			return fmt.Errorf("添加默认路由 %s 失败：%v", prefix, err)
		}
	}
	return nil
}

func removeSplitRoutes(dev string) {
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		_, _ = runHidden("ip", "route", "del", prefix, "dev", dev)
	}
}

func addBypassRoute(ip, gateway, dev string) error {
	if _, err := runHidden("ip", "route", "replace", ip+"/32", "via", gateway, "dev", dev); err != nil {
		return fmt.Errorf("添加旁路路由 %s 失败：%v", ip, err)
	}
	return nil
}

func deleteBypassRoute(ip string) {
	_, _ = runHidden("ip", "route", "del", ip+"/32")
}

/* ----------------------------------------------------------------- setup */

// resolvConfServers lists the system's DNS servers: they stay on the physical
// path while TUN owns the default route.
func resolvConfServers() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "nameserver" {
			if ip := net.ParseIP(f[1]).To4(); ip != nil {
				out = append(out, ip.String())
			}
		}
	}
	return out
}

func configureTunInterface(name string) error {
	_, _ = runHidden("ip", "link", "set", "dev", name, "up")

	var lastErr error
	assigned := false
	for i := 0; i < 10; i++ {
		if _, err := runHidden("ip", "addr", "replace", tunAddress+"/15", "dev", name); err == nil {
			lastErr = nil
			assigned = true
			break
		} else {
			lastErr = err
		}
		time.Sleep(400 * time.Millisecond)
	}
	if !assigned {
		return fmt.Errorf("给虚拟网卡设置地址失败：%v", lastErr)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		if _, up, hasIP, _ := linuxInterface(name); up && hasIP {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("虚拟网卡状态没有生效（" + tunAddress + " 未生效或接口未 UP）")
		}
		time.Sleep(200 * time.Millisecond)
	}

	_, _ = runHidden("ip", "link", "set", "dev", name, "mtu", "1400")
	return nil
}

func startTunPlatform(t *Tunnel, p Profile, s Settings) (*tunSession, error) {
	if !isElevated() {
		return nil, errors.New("需要 root 权限：请用 sudo 运行 tcp2rdp-web")
	}
	tool, err := findTunTool()
	if err != nil {
		return nil, err
	}
	target := dataPathTarget(s)
	serverIPs := resolveServerIPv4(p.Server)

	// Double dashes matter: tun2socks uses pflag, where "-device" means
	// "-d evice" and quietly creates an adapter with the wrong name.
	cmd := exec.Command(tool,
		"--device", "tun://"+tunAdapterName,
		"--proxy", "socks5://"+p.DialAddr(),
		"--loglevel", "info",
		"--mtu", "1400",
		"--udp-timeout", "60s",
	)
	cmd.Dir = filepath.Dir(tool)
	cmd.SysProcAttr = hiddenProcAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 tun2socks 失败：%v", err)
	}

	sess := &tunSession{
		cmd:       cmd,
		pid:       cmd.Process.Pid,
		adapter:   tunAdapterName,
		tunIP:     tunAddress,
		serverIPs: serverIPs,
		closed:    make(chan struct{}),
		statePath: tunStatePath(t.store),
	}
	t.logs.debugf("tun2socks 已启动（pid %d）", sess.pid)
	go t.readTunLog(stdout)
	go t.readTunLog(stderr)
	go func() {
		werr := cmd.Wait()
		close(sess.closed)
		t.onTunExit(sess, werr)
	}()
	// Write the owner early: if this process dies during setup, the next launch
	// still knows which tun2socks to kill.
	_ = saveTunState(t.store, tunState{AppPID: os.Getpid(), TunPID: sess.pid, Adapter: sess.adapter})

	deadline := time.Now().Add(10 * time.Second)
	for {
		ifIndex, _, _, ierr := linuxInterface(tunAdapterName)
		if ierr == nil {
			sess.ifIndex = ifIndex
			break
		}
		if !processAlive(sess.pid) {
			_ = stopTunPlatform(sess)
			return nil, errors.New("tun2socks 启动后立即退出：请看会话日志里的 TUN 行")
		}
		if time.Now().After(deadline) {
			_ = stopTunPlatform(sess)
			return nil, errors.New("创建虚拟网卡超时：请确认以 root 运行且 /dev/net/tun 可用")
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := configureTunInterface(tunAdapterName); err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}

	routes, err := linuxRoutes()
	if err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}
	physDev, gateway, err := physicalDefaultRoute(routes, tunAdapterName)
	if err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}

	// Keep the server and DNS on the physical device. A DNS server already in
	// the LAN is covered by its on-link route and needs no /32.
	var candidates []string
	candidates = append(candidates, serverIPs...)
	candidates = append(candidates, resolvConfServers()...)
	planned := map[string]bool{}
	var added []tunBypass
	for _, ip := range candidates {
		if ip == "" || planned[ip] {
			continue
		}
		if coveredByPhysical(routes, ip, tunAdapterName) {
			continue
		}
		if err := addBypassRoute(ip, gateway, physDev); err != nil {
			for _, x := range added {
				deleteBypassRoute(x.IP)
			}
			_ = stopTunPlatform(sess)
			return nil, err
		}
		added = append(added, tunBypass{IP: ip})
		planned[ip] = true
	}
	sess.bypass = added
	_ = saveTunState(t.store, tunState{AppPID: os.Getpid(), TunPID: sess.pid, Adapter: sess.adapter, Bypass: added})

	if err := addSplitRoutes(tunAdapterName); err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}
	time.Sleep(250 * time.Millisecond)
	if !hasSplitRoutes(tunAdapterName) {
		_ = stopTunPlatform(sess)
		return nil, errors.New("默认路由没有生效：0.0.0.0/1 与 128.0.0.0/1 未指向 TUN")
	}

	d, err := waitTCPViaTun(target, 10*time.Second)
	if err != nil {
		_ = stopTunPlatform(sess)
		return nil, fmt.Errorf("TUN 路由已建立，但经 TUN 连接 %s 失败：%v", target, err)
	}
	t.logs.debugf("TUN 数据检查通过：经 TUN 连接 %s 用时 %d ms", target, d.Milliseconds())
	return sess, nil
}

func stopTunPlatform(s *tunSession) error {
	s.stopOnce.Do(func() {
		removeSplitRoutes(s.adapter)
		for _, b := range s.bypass {
			deleteBypassRoute(b.IP)
		}
		if s.statePath != "" {
			_ = os.Remove(s.statePath)
		}
	})
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.closed:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("tun2socks 没有在 3 秒内退出")
	}
}

/* -------------------------------------------------------------- self-test */

func testTunPlatform(s *tunSession, target string) TunReport {
	rep := TunReport{Target: target, At: time.Now().Format("15:04:05")}
	add := func(name string, ok bool, detail string, optional ...bool) {
		rep.Checks = append(rep.Checks, TunCheck{Name: name, OK: ok, Detail: detail, Optional: len(optional) > 0 && optional[0]})
	}

	add("root 权限", isElevated(), "已提升")
	alive := processAlive(s.pid)
	comm := ""
	if alive {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", s.pid))
		comm = strings.TrimSpace(string(b))
	}
	add("tun2socks 进程", alive && comm == tunToolName, fmt.Sprintf("PID %d", s.pid))

	ifIndex, up, hasIP, ierr := linuxInterface(s.adapter)
	detail := s.adapter + " 未找到"
	if ierr == nil {
		detail = fmt.Sprintf("%s · ifIndex %d", s.adapter, ifIndex)
	}
	add("虚拟网卡", ierr == nil && up, detail)
	add("接口地址", hasIP, tunAddress+"/15")

	routesOK := hasSplitRoutes(s.adapter)
	add("默认路由", routesOK, "0.0.0.0/1 + 128.0.0.0/1 → "+s.adapter)

	routes, routeErr := linuxRoutes()
	if routeErr != nil {
		add("物理网关", false, routeErr.Error())
	} else if dev, gw, err := physicalDefaultRoute(routes, s.adapter); err != nil {
		add("物理网关", false, err.Error())
	} else {
		add("物理网关", true, fmt.Sprintf("%s · %s", dev, gw))
	}

	if routeErr != nil {
		add("服务器旁路", false, routeErr.Error())
	} else if len(s.serverIPs) == 0 {
		add("服务器旁路", true, "直连节点没有服务器地址")
	} else {
		var bad []string
		for _, ip := range s.serverIPs {
			if dev, ok := bestRouteDev(routes, ip); !ok || dev == s.adapter {
				bad = append(bad, ip)
			}
		}
		detail = "服务器 " + strings.Join(s.serverIPs, "、") + " 走物理网卡"
		if len(bad) > 0 {
			detail = "这些地址仍会被 TUN 接走：" + strings.Join(bad, "、")
		}
		add("服务器旁路", len(bad) == 0, detail)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	hosts, lookupErr := net.DefaultResolver.LookupHost(ctx, "www.baidu.com")
	cancel()
	if lookupErr != nil {
		add("DNS 解析", false, truncate(lookupErr.Error(), 160))
	} else {
		add("DNS 解析", true, "www.baidu.com → "+strings.Join(hosts, "、"))
	}

	tcpDur, tcpErr := tcpViaTun(target, 8*time.Second)
	tcpOK := tcpErr == nil
	if tcpErr != nil {
		add("TCP 经 TUN", false, truncate(tcpErr.Error(), 160))
	} else {
		add("TCP 经 TUN", true, fmt.Sprintf("连接 %s 用时 %d ms", target, tcpDur.Milliseconds()))
	}

	// A 443 target gets a real byte-level check: completing a TLS handshake
	// through the TUN socket means payloads travel end to end, not just a
	// locally answered SYN.
	if _, port, perr := net.SplitHostPort(target); perr == nil && port == "443" {
		if d, err := tlsViaTun(target, 10*time.Second); err != nil {
			add("TLS 经 TUN", false, truncate(err.Error(), 160))
		} else {
			add("TLS 经 TUN", true, fmt.Sprintf("完成 %s 的 TLS 握手，用时 %d ms", target, d.Milliseconds()))
		}
	}

	if err := dnsViaTun(4 * time.Second); err != nil {
		add("UDP 经 TUN", false, "1.1.1.1:53 无回应："+truncate(err.Error(), 120), true)
	} else {
		add("UDP 经 TUN", true, "DNS 查询 1.1.1.1:53 得到回应", true)
	}

	for _, c := range rep.Checks {
		if !c.OK && !c.Optional {
			rep.OK = false
			rep.Summary = summarizeTunReport(rep.Checks, target, tcpDur, tcpOK)
			return rep
		}
	}
	rep.OK = true
	rep.Summary = summarizeTunReport(rep.Checks, target, tcpDur, tcpOK)
	return rep
}

/* --------------------------------------------------------- stale cleanup */

func cleanupStaleTunPlatform(store *Store, logf func(string, ...any)) {
	if !isElevated() {
		return
	}
	path := tunStatePath(store)
	st, err := loadTunState(store)
	if err != nil {
		if _, _, _, ierr := linuxInterface(tunAdapterName); ierr == nil {
			logf("发现残留的 TUN 网卡，清理默认路由")
			removeSplitRoutes(tunAdapterName)
		}
		if !os.IsNotExist(err) {
			_ = os.Remove(path)
		}
		return
	}
	if st.AppPID > 0 && processAlive(st.AppPID) {
		logf("TUN 正由另一个实例使用，跳过清理")
		return
	}
	logf("清理上次异常退出留下的 TUN 状态…")
	if st.TunPID > 0 && processAlive(st.TunPID) {
		_ = killProcess(st.TunPID)
	}
	adapter := st.Adapter
	if adapter == "" {
		adapter = tunAdapterName
	}
	removeSplitRoutes(adapter)
	for _, b := range st.Bypass {
		deleteBypassRoute(b.IP)
	}
	_ = os.Remove(path)
	logf("残留的 TUN 路由已清理")
}
