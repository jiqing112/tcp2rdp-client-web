//go:build windows

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
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tunToolName   = "tun2socks.exe"
	wintunDLLName = "wintun.dll"
	stillActive   = 259 // STILL_ACTIVE
)

func tunPlatformSupported() bool { return true }

func tunPlatformCheck() error {
	if !isElevated() {
		return errors.New("TUN 模式需要以管理员身份启动：请关闭程序后右键「以管理员身份运行」")
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
	return "", errors.New("没有找到 tun2socks.exe：它应和 wintun.dll 一起放在程序目录")
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

func runHidden(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

/* ------------------------------------------------------------ adapters */

type tunAdapter struct {
	Name    string
	IfIndex uint32
	Up      bool
	IPs     []net.IP
	DNS     []net.IP
}

// listAdapters copies everything needed out of the API buffer: the pointers are
// only valid until the buffer is reused.
func listAdapters() ([]tunAdapter, error) {
	size := uint32(16 * 1024)
	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		flags := uint32(windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_INCLUDE_GATEWAYS)
		err := windows.GetAdaptersAddresses(windows.AF_INET, flags, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil, err
		}
		var out []tunAdapter
		for a := first; a != nil; a = a.Next {
			item := tunAdapter{
				Name:    windows.UTF16PtrToString(a.FriendlyName),
				IfIndex: a.IfIndex,
				Up:      a.OperStatus == uint32(windows.IfOperStatusUp),
			}
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if u.Address.Sockaddr == nil {
					continue
				}
				if ip := u.Address.IP(); ip != nil {
					item.IPs = append(item.IPs, ip)
				}
			}
			for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
				if d.Address.Sockaddr == nil {
					continue
				}
				if ip := d.Address.IP(); ip != nil && ip.To4() != nil {
					item.DNS = append(item.DNS, ip)
				}
			}
			out = append(out, item)
		}
		return out, nil
	}
	return nil, errors.New("枚举网络适配器失败：缓冲区不足")
}

func adapterByName(name string) (tunAdapter, bool) {
	adapters, err := listAdapters()
	if err != nil {
		return tunAdapter{}, false
	}
	for _, a := range adapters {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return tunAdapter{}, false
}

func adapterHasIP(a tunAdapter, want string) bool {
	wantIP := net.ParseIP(want)
	for _, ip := range a.IPs {
		if v4 := ip.To4(); v4 != nil && wantIP != nil && v4.Equal(wantIP) {
			return true
		}
	}
	return false
}

/* -------------------------------------------------------------- routes */

func ipv4Routes() ([]windows.MibIpForwardRow2, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		return nil, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := table.Rows()
	out := make([]windows.MibIpForwardRow2, len(rows))
	copy(out, rows)
	return out, nil
}

func rawIPv4(addr windows.RawSockaddrInet) net.IP {
	if addr.Family != windows.AF_INET {
		return nil
	}
	b := (*[4]byte)(unsafe.Pointer(&addr.Data[0]))
	return net.IPv4(b[0], b[1], b[2], b[3])
}

func prefixIP(prefix windows.IpAddressPrefix) net.IP {
	return rawIPv4(prefix.Prefix)
}

// physicalDefaultRoute picks the lowest-metric default route that is not the
// TUN adapter. It is the path kept for the tunnel server and DNS.
func physicalDefaultRoute(rows []windows.MibIpForwardRow2, tunIf uint32) (uint32, string, error) {
	var best *windows.MibIpForwardRow2
	for i := range rows {
		r := &rows[i]
		if int(r.DestinationPrefix.PrefixLength) != 0 || r.Loopback != 0 || r.InterfaceIndex == tunIf {
			continue
		}
		if gw := rawIPv4(r.NextHop); gw == nil || gw.IsUnspecified() {
			continue
		}
		if best == nil || r.Metric < best.Metric {
			best = r
		}
	}
	if best == nil {
		return 0, "", errors.New("没有找到可用的物理默认网关")
	}
	return best.InterfaceIndex, rawIPv4(best.NextHop).String(), nil
}

// coveredByPhysical answers "would this address already leave through a real
// adapter, using a route more specific than the /1 split TUN installs?" DNS
// servers in the LAN and a LAN tunnel server need no extra /32.
func coveredByPhysical(rows []windows.MibIpForwardRow2, ipStr string, tunIf uint32) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	ip = ip.To4()
	if ip == nil {
		return false
	}
	bestLen := -1
	bestIf := uint32(0)
	for i := range rows {
		r := &rows[i]
		plen := int(r.DestinationPrefix.PrefixLength)
		if plen <= 1 || plen > 32 || r.Loopback != 0 {
			continue
		}
		dest := prefixIP(r.DestinationPrefix)
		if dest == nil {
			continue
		}
		mask := net.CIDRMask(plen, 32)
		if !ip.Mask(mask).Equal(dest.Mask(mask)) {
			continue
		}
		if plen > bestLen {
			bestLen = plen
			bestIf = r.InterfaceIndex
		}
	}
	return bestLen > 0 && bestIf != tunIf
}

// bestRouteFor follows longest-prefix matching, which is how Windows decides
// where a destination really goes.
func bestRouteFor(rows []windows.MibIpForwardRow2, ipStr string) (uint32, int, bool) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0, 0, false
	}
	ip = ip.To4()
	if ip == nil {
		return 0, 0, false
	}
	bestLen := -1
	bestIf := uint32(0)
	for i := range rows {
		r := &rows[i]
		plen := int(r.DestinationPrefix.PrefixLength)
		if plen < 0 || plen > 32 || r.Loopback != 0 {
			continue
		}
		dest := prefixIP(r.DestinationPrefix)
		if dest == nil {
			continue
		}
		mask := net.CIDRMask(plen, 32)
		if !ip.Mask(mask).Equal(dest.Mask(mask)) {
			continue
		}
		if plen > bestLen {
			bestLen = plen
			bestIf = r.InterfaceIndex
		}
	}
	return bestIf, bestLen, bestLen >= 0
}

func hasSplitRoutes(ifIndex uint32) bool {
	rows, err := ipv4Routes()
	if err != nil {
		return false
	}
	return routeInstalled(rows, "0.0.0.0", 1, ifIndex) && routeInstalled(rows, "128.0.0.0", 1, ifIndex)
}

func routeInstalled(rows []windows.MibIpForwardRow2, dest string, plen int, ifIndex uint32) bool {
	want := net.ParseIP(dest).To4()
	for i := range rows {
		r := &rows[i]
		if int(r.DestinationPrefix.PrefixLength) != plen || r.InterfaceIndex != ifIndex {
			continue
		}
		if got := prefixIP(r.DestinationPrefix); got != nil && got.Equal(want) {
			return true
		}
	}
	return false
}

func addSplitRoutes(name string) error {
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		_, err := runHidden("netsh", "interface", "ipv4", "add", "route", "prefix="+prefix, "interface="+name, "metric=1", "store=active")
		if err == nil {
			continue
		}
		// A stale route from a previous run can block the add; replace it.
		_, _ = runHidden("netsh", "interface", "ipv4", "delete", "route", "prefix="+prefix, "interface="+name, "store=active")
		if _, err2 := runHidden("netsh", "interface", "ipv4", "add", "route", "prefix="+prefix, "interface="+name, "metric=1", "store=active"); err2 != nil {
			return fmt.Errorf("添加默认路由 %s 失败：%v", prefix, err)
		}
	}
	return nil
}

func removeSplitRoutes(name string) {
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		_, _ = runHidden("netsh", "interface", "ipv4", "delete", "route", "prefix="+prefix, "interface="+name, "store=active")
	}
}

func addBypassRoute(b tunBypass, gateway string) error {
	if _, err := runHidden("route", "add", b.IP, "mask", "255.255.255.255", gateway, "metric", "1", "if", strconv.FormatUint(uint64(b.IfIndex), 10)); err != nil {
		return fmt.Errorf("添加旁路路由 %s 失败：%v", b.IP, err)
	}
	return nil
}

func deleteBypassRoute(b tunBypass) {
	_, _ = runHidden("route", "delete", b.IP, "mask", "255.255.255.255", "if", strconv.FormatUint(uint64(b.IfIndex), 10))
}

func setInterfaceMetric(ifIndex uint32, metric int) error {
	script := fmt.Sprintf("Set-NetIPInterface -InterfaceIndex %d -AddressFamily IPv4 -InterfaceMetric %d -ErrorAction Stop", ifIndex, metric)
	if _, err := runHidden("powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
		// Some locked-down images restrict PowerShell; netsh accepts metric on
		// builds where the help text omits it.
		if _, err2 := runHidden("netsh", "interface", "ipv4", "set", "interface", "interface="+strconv.FormatUint(uint64(ifIndex), 10), "metric="+strconv.Itoa(metric)); err2 != nil {
			return err
		}
	}
	return nil
}

/* --------------------------------------------------------------- setup */

func configureTunInterface(name string, ifIndex uint32) error {
	_, _ = runHidden("netsh", "interface", "set", "interface", "name="+name, "admin=enable")

	var lastErr error
	assigned := false
	for i := 0; i < 10; i++ {
		if _, err := runHidden("netsh", "interface", "ipv4", "set", "address", "name="+name, "static", tunAddress, tunMask); err == nil {
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
		if a, ok := adapterByName(name); ok && adapterHasIP(a, tunAddress) {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("虚拟网卡的 " + tunAddress + " 地址没有生效")
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := setInterfaceMetric(ifIndex, 1); err != nil {
		return fmt.Errorf("设置 TUN 接口跃点失败：%v", err)
	}
	_, _ = runHidden("netsh", "interface", "ipv4", "set", "subinterface", "name="+name, "mtu=1400", "store=active")
	return nil
}

func startTunPlatform(t *Tunnel, p Profile, s Settings) (*tunSession, error) {
	if !isElevated() {
		return nil, errors.New("需要管理员权限：请关闭程序后右键「以管理员身份运行」")
	}
	tool, err := findTunTool()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(tool), wintunDLLName)); err != nil {
		return nil, errors.New("wintun.dll 不在 tun2socks.exe 旁边：请把两个文件放在一起")
	}

	target := dataPathTarget(s)
	serverIPs := resolveServerIPv4(p.Server)
	adapters, err := listAdapters()
	if err != nil {
		return nil, fmt.Errorf("读取网络适配器失败：%v", err)
	}

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
	var adapter tunAdapter
	for {
		if a, ok := adapterByName(tunAdapterName); ok {
			adapter = a
			break
		}
		if !processAlive(sess.pid) {
			_ = stopTunPlatform(sess)
			return nil, errors.New("tun2socks 启动后立即退出：请看会话日志里的 TUN 行")
		}
		if time.Now().After(deadline) {
			_ = stopTunPlatform(sess)
			return nil, errors.New("创建虚拟网卡超时：请确认 wintun.dll 可用且已以管理员身份运行")
		}
		time.Sleep(200 * time.Millisecond)
	}
	sess.ifIndex = adapter.IfIndex

	if err := configureTunInterface(tunAdapterName, adapter.IfIndex); err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}

	rows, err := ipv4Routes()
	if err != nil {
		_ = stopTunPlatform(sess)
		return nil, fmt.Errorf("读取路由表失败：%v", err)
	}
	physIf, gateway, err := physicalDefaultRoute(rows, adapter.IfIndex)
	if err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}

	// Keep the server and DNS on the physical adapter. A DNS server already in
	// the LAN is covered by its on-link route and needs no /32. The DNS servers
	// are only bypassed, not configured on the TUN adapter: advertising them
	// there makes Windows bind queries to the TUN interface, where a proxy that
	// cannot carry UDP silently blackholes every lookup.
	var dns []net.IP
	for _, a := range adapters {
		if a.IfIndex == physIf {
			dns = a.DNS
			break
		}
	}
	planned := map[string]tunBypass{}
	var candidates []string
	candidates = append(candidates, serverIPs...)
	for _, ip := range dns {
		candidates = append(candidates, ip.String())
	}
	for _, ip := range candidates {
		if ip == "" {
			continue
		}
		if _, ok := planned[ip]; ok {
			continue
		}
		if coveredByPhysical(rows, ip, adapter.IfIndex) {
			continue
		}
		planned[ip] = tunBypass{IP: ip, IfIndex: physIf}
	}
	var added []tunBypass
	for _, b := range planned {
		if err := addBypassRoute(b, gateway); err != nil {
			for _, x := range added {
				deleteBypassRoute(x)
			}
			_ = stopTunPlatform(sess)
			return nil, err
		}
		added = append(added, b)
	}
	sess.bypass = added
	_ = saveTunState(t.store, tunState{AppPID: os.Getpid(), TunPID: sess.pid, Adapter: sess.adapter, Bypass: added})

	if err := addSplitRoutes(tunAdapterName); err != nil {
		_ = stopTunPlatform(sess)
		return nil, err
	}
	time.Sleep(250 * time.Millisecond)
	if !hasSplitRoutes(adapter.IfIndex) {
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
			deleteBypassRoute(b)
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

func testTunPlatform(s *tunSession, target string) TunReport {
	rep := TunReport{Target: target, At: time.Now().Format("15:04:05")}
	add := func(name string, ok bool, detail string, optional ...bool) {
		rep.Checks = append(rep.Checks, TunCheck{Name: name, OK: ok, Detail: detail, Optional: len(optional) > 0 && optional[0]})
	}

	add("管理员权限", isElevated(), "已提升")
	alive := processAlive(s.pid)
	image := ""
	if alive {
		image = processImagePath(s.pid)
	}
	add("tun2socks 进程", alive && strings.EqualFold(filepath.Base(image), tunToolName),
		fmt.Sprintf("PID %d", s.pid))

	adapter, found := adapterByName(s.adapter)
	up := found && adapter.Up
	detail := s.adapter + " 未找到"
	if found {
		detail = fmt.Sprintf("%s · ifIndex %d", adapter.Name, adapter.IfIndex)
	}
	add("虚拟网卡", up, detail)

	hasIP := found && adapterHasIP(adapter, tunAddress)
	add("接口地址", hasIP, tunAddress+"/15")

	routesOK := hasSplitRoutes(s.ifIndex)
	add("默认路由", routesOK, "0.0.0.0/1 + 128.0.0.0/1 → "+s.adapter)

	rows, routeErr := ipv4Routes()
	if routeErr != nil {
		add("物理网关", false, routeErr.Error())
	} else if physIf, gw, err := physicalDefaultRoute(rows, s.ifIndex); err != nil {
		add("物理网关", false, err.Error())
	} else {
		add("物理网关", true, fmt.Sprintf("ifIndex %d · %s", physIf, gw))
	}

	if routeErr != nil {
		add("服务器旁路", false, routeErr.Error())
	} else if len(s.serverIPs) == 0 {
		add("服务器旁路", true, "直连节点没有服务器地址")
	} else {
		var bad []string
		for _, ip := range s.serverIPs {
			if ifIdx, _, ok := bestRouteFor(rows, ip); !ok || ifIdx == s.ifIndex {
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

/* -------------------------------------------------------- stale cleanup */

func cleanupStaleTunPlatform(store *Store, logf func(string, ...any)) {
	if !isElevated() {
		return
	}
	path := tunStatePath(store)
	st, err := loadTunState(store)
	if err != nil {
		if _, ok := adapterByName(tunAdapterName); ok {
			logf("发现残留的 TUN 网卡，清理默认路由")
			removeSplitRoutes(tunAdapterName)
		}
		if err != nil && !os.IsNotExist(err) {
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
		if strings.EqualFold(filepath.Base(processImagePath(st.TunPID)), tunToolName) {
			_ = killProcess(st.TunPID)
		}
	}
	adapter := st.Adapter
	if adapter == "" {
		adapter = tunAdapterName
	}
	removeSplitRoutes(adapter)
	for _, b := range st.Bypass {
		deleteBypassRoute(b)
	}
	_ = os.Remove(path)
	logf("残留的 TUN 路由已清理")
}
