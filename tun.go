package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	tunAdapterName = "tcp2rdp-tun"
	tunAddress     = "198.18.0.1"
	tunMask        = "255.254.0.0" // /15
)

// TunCheck is one line of the TUN self-test.
type TunCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
	Optional bool   `json:"optional"`
}

// TunReport is what the "运行自检" button shows: several independent checks,
// because "the switch is on" says nothing about whether traffic really flows.
type TunReport struct {
	OK      bool       `json:"ok"`
	Summary string     `json:"summary"`
	Target  string     `json:"target"`
	At      string     `json:"at"`
	Checks  []TunCheck `json:"checks"`
}

// tunBypass is a host route kept on the physical adapter while TUN owns the
// default: the tunnel server itself and the DNS servers must never be sent back
// into the tunnel.
type tunBypass struct {
	IP      string `json:"ip"`
	IfIndex uint32 `json:"ifIndex"`
}

// tunState is written into the data directory so a crashed session can be
// cleaned up by the next launch: stale default routes would otherwise leave the
// machine without internet.
type tunState struct {
	AppPID  int         `json:"appPid"`
	TunPID  int         `json:"tunPid"`
	Adapter string      `json:"adapter"`
	Bypass  []tunBypass `json:"bypass"`
}

// tunSession is one running tun2socks plus the routes it owns.
type tunSession struct {
	cmd       *exec.Cmd
	pid       int
	adapter   string
	ifIndex   uint32
	tunIP     string
	serverIPs []string
	bypass    []tunBypass
	closed    chan struct{}
	stopOnce  sync.Once
	statePath string
}

func tunStatePath(store *Store) string {
	return filepath.Join(filepath.Dir(store.Path()), "tun-state.json")
}

func saveTunState(store *Store, st tunState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tunStatePath(store), data, 0o644)
}

func loadTunState(store *Store) (tunState, error) {
	var st tunState
	data, err := os.ReadFile(tunStatePath(store))
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

// SetTun records the user's TUN preference. Like the system proxy switch it may
// be turned on before connecting; the routes are only installed once the tunnel
// has proved it can carry data. Turning it off removes the routes immediately.
func (t *Tunnel) SetTun(on bool) (Status, error) {
	s := t.Settings().Normalize()
	if on {
		if !s.Socks5 {
			return t.Status(), errors.New("TUN 模式需要 SOCKS5 代理模式")
		}
		if err := tunPlatformCheck(); err != nil {
			return t.Status(), err
		}
	}
	s.TunEnabled = on

	t.mu.Lock()
	t.settings = s
	t.loaded = true
	running := t.state == StateRunning
	stop := t.stop
	if on {
		if running {
			t.tunMsg = "等待隧道就绪后启用"
		} else {
			t.tunMsg = "已预设：连通后自动启用"
		}
	} else {
		t.tunMsg = ""
	}
	t.mu.Unlock()

	if err := s.Save(t.store); err != nil {
		return t.Status(), err
	}

	if !on {
		t.logs.debugf("TUN 模式已关闭")
		t.stopTun()
		t.emitStatus()
		return t.Status(), nil
	}
	if !running {
		t.logs.debugf("TUN 模式已预设：隧道连通后自动启用")
		t.emitStatus()
		return t.Status(), nil
	}
	t.logs.debugf("TUN 模式已开启：等待隧道能承载数据后接管路由")
	t.startTunWaiter(stop)
	t.emitStatus()
	return t.Status(), nil
}

// startTunWaiter waits for a working data path, then installs TUN. Probing the
// tunnel first is what keeps a preset from cutting the machine off when the
// server is unreachable.
func (t *Tunnel) startTunWaiter(stop chan struct{}) {
	if stop == nil {
		return
	}
	t.mu.Lock()
	if t.tunWait || t.tunOn || t.tun != nil {
		t.mu.Unlock()
		return
	}
	t.tunWait = true
	t.mu.Unlock()

	go func() {
		defer func() {
			t.mu.Lock()
			t.tunWait = false
			t.mu.Unlock()
		}()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		var lastLog time.Time
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			t.mu.Lock()
			wanted := t.settings.TunEnabled
			running := t.state == StateRunning
			active := t.tunOn
			profile := t.profile
			settings := t.settings
			t.mu.Unlock()
			if !wanted || !running || active || profile.Mode != "socks5" {
				return
			}
			if _, err := probeDataPath(profile.DialAddr(), dataPathTarget(settings), 5*time.Second); err != nil {
				if time.Since(lastLog) > 20*time.Second {
					t.logs.debugf("TUN 等待隧道就绪：%v", err)
					lastLog = time.Now()
				}
				continue
			}
			if err := t.installTun(); err != nil {
				t.failTun(err)
			}
			return
		}
	}()
}

// installTun creates the adapter, routes, and runs the automatic data check.
// The mutex serializes it with stop and self-test.
func (t *Tunnel) installTun() error {
	t.tunMu.Lock()
	defer t.tunMu.Unlock()

	t.mu.Lock()
	if t.tunOn || t.tun != nil {
		t.mu.Unlock()
		return nil
	}
	if t.state != StateRunning || !t.settings.TunEnabled {
		t.mu.Unlock()
		return nil
	}
	profile := t.profile
	settings := t.settings
	t.tunMsg = "正在创建虚拟网卡…"
	t.mu.Unlock()
	t.emitStatus()

	platform := platformName()
	t.logs.debugf("正在启用 TUN（tun2socks · %s）…", platform)
	sess, err := startTunPlatform(t, profile, settings)
	if err != nil {
		t.mu.Lock()
		t.tunOn = false
		t.tunMsg = "TUN 启用失败：" + truncate(err.Error(), 180)
		t.mu.Unlock()
		t.logs.debugf("TUN 启用失败：%v", err)
		t.emitStatus()
		return err
	}

	t.mu.Lock()
	t.tun = sess
	t.tunOn = true
	t.tunMsg = "已接管 IPv4 流量 · 可运行自检"
	t.mu.Unlock()
	t.logs.debugf("TUN 已启用：%s 接管 IPv4 默认路由", sess.adapter)
	t.emitStatus()
	return nil
}

// failTun turns the preference back off after a failed setup: leaving a preset
// on that cannot be installed would retry forever.
func (t *Tunnel) failTun(err error) {
	t.mu.Lock()
	settings := t.settings
	settings.TunEnabled = false
	t.settings = settings
	t.mu.Unlock()
	_ = settings.Save(t.store)
	t.logs.debugf("TUN 已自动关闭（启用失败）：%v", err)
	t.emitStatus()
}

// onTunExit handles tun2socks dying on its own. A process stopped on purpose is
// already detached (t.tun is nil), so this only fires for crashes.
func (t *Tunnel) onTunExit(sess *tunSession, err error) {
	t.tunMu.Lock()
	defer t.tunMu.Unlock()

	t.mu.Lock()
	if t.tun != sess {
		t.mu.Unlock()
		return
	}
	t.tun = nil
	t.tunOn = false
	settings := t.settings
	settings.TunEnabled = false
	t.settings = settings
	t.tunMsg = "tun2socks 已退出，TUN 路由已撤销"
	t.mu.Unlock()

	_ = settings.Save(t.store)
	if cerr := stopTunPlatform(sess); cerr != nil {
		t.logs.debugf("撤销 TUN 路由时出错：%v", cerr)
	}
	t.logs.debugf("tun2socks 意外退出，TUN 已撤销：%v", err)
	t.logs.debugf("TUN 开关已自动关闭；可重新打开再试一次")
	t.emitStatus()
}

// stopTun removes the routes synchronously, then kills tun2socks. It is safe to
// call when TUN was never started, and is called on stop, quit, and exit.
func (t *Tunnel) stopTun() {
	t.tunMu.Lock()
	t.mu.Lock()
	sess := t.tun
	t.tun = nil
	wasOn := t.tunOn
	t.tunOn = false
	t.mu.Unlock()

	if sess == nil {
		t.tunMu.Unlock()
		return
	}
	if err := stopTunPlatform(sess); err != nil {
		t.logs.debugf("撤销 TUN 时出错：%v", err)
	}
	t.mu.Lock()
	t.tunMsg = ""
	t.mu.Unlock()
	t.tunMu.Unlock()
	if wasOn {
		t.logs.debugf("TUN 已撤销，IPv4 路由已恢复")
	}
	t.emitStatus()
}

// TestTun runs the multi-point self-test. It holds tunMu so the session cannot
// be torn down while its routes and process are being checked.
func (t *Tunnel) TestTun() (TunReport, error) {
	t.tunMu.Lock()
	defer t.tunMu.Unlock()

	t.mu.Lock()
	sess := t.tun
	active := t.tunOn
	settings := t.settings
	t.mu.Unlock()
	if sess == nil || !active {
		return TunReport{}, errors.New("TUN 还没启用：先打开开关，等隧道就绪后再自检")
	}
	report := testTunPlatform(sess, dataPathTarget(settings))
	t.mu.Lock()
	t.tunMsg = report.Summary
	t.mu.Unlock()
	t.emitStatus()
	return report, nil
}

// CleanupStaleTun repairs what a crashed previous run may have left behind:
// routes pointing at a dead adapter, or a tun2socks process nobody owns.
func (t *Tunnel) CleanupStaleTun() {
	cleanupStaleTunPlatform(t.store, t.logs.debugf)
}

func (t *Tunnel) readTunLog(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 32*1024), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(reANSI.ReplaceAllString(scanner.Text(), ""))
		if line != "" {
			t.logs.WriteLine("TUN  " + line)
		}
	}
}

func resolveServerIPv4(server string) []string {
	host, _, ok := splitHostPort(server)
	if !ok {
		return nil
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return []string{v4.String()}
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil && !seen[v4.String()] {
			seen[v4.String()] = true
			out = append(out, v4.String())
		}
	}
	return out
}

func tcpViaTun(target string, timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{
		Timeout:   timeout,
		LocalAddr: &net.TCPAddr{IP: net.ParseIP(tunAddress)},
	}
	start := time.Now()
	conn, err := d.Dial("tcp", target)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

// tlsViaTun completes a TLS handshake through the TUN socket. A bare TCP dial
// through a userspace stack is answered locally and returns in 0 ms even when
// the tunnel is dead; a ClientHello/ServerHello exchange proves bytes actually
// crossed the tunnel in both directions.
func tlsViaTun(target string, timeout time.Duration) (time.Duration, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout, LocalAddr: &net.TCPAddr{IP: net.ParseIP(tunAddress)}},
		Config:    &tls.Config{ServerName: host, InsecureSkipVerify: true},
	}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

func waitTCPViaTun(target string, budget time.Duration) (time.Duration, error) {
	var lastErr error
	end := time.Now().Add(budget)
	for {
		d, err := tcpViaTun(target, 4*time.Second)
		if err == nil {
			return d, nil
		}
		lastErr = err
		if time.Now().Add(600 * time.Millisecond).After(end) {
			return 0, lastErr
		}
		time.Sleep(600 * time.Millisecond)
	}
}

func buildDNSQuery(id uint16, name string) []byte {
	b := make([]byte, 12, 12+len(name)+6)
	binary.BigEndian.PutUint16(b[0:2], id)
	b[2] = 0x01 // recursion desired
	binary.BigEndian.PutUint16(b[4:6], 1)
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0x00, 0x00, 0x01, 0x00, 0x01)
	return b
}

// dnsViaTun sends a real DNS query with its source bound to the TUN address, so
// it can only leave through tun2socks' UDP path.
func dnsViaTun(timeout time.Duration) error {
	conn, err := net.DialUDP("udp",
		&net.UDPAddr{IP: net.ParseIP(tunAddress)},
		&net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 53},
	)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	id := uint16(time.Now().UnixNano())
	if _, err := conn.Write(buildDNSQuery(id, "www.example.com")); err != nil {
		return err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	if n < 12 || binary.BigEndian.Uint16(buf[:2]) != id {
		return errors.New("DNS 回包不匹配")
	}
	if rcode := buf[3] & 0x0f; rcode != 0 {
		return fmt.Errorf("DNS 返回错误码 %d", rcode)
	}
	return nil
}

// summarizeTunReport turns checks into the one line shown on the dashboard.
func summarizeTunReport(checks []TunCheck, target string, tcp time.Duration, tcpOK bool) string {
	var failed []string
	required, passed := 0, 0
	for _, c := range checks {
		if c.Optional {
			continue
		}
		required++
		if c.OK {
			passed++
		} else {
			failed = append(failed, c.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Sprintf("%d/%d 项通过 · 未通过：%s", passed, required, strings.Join(failed, "、"))
	}
	if tcpOK {
		return fmt.Sprintf("%d 项全部通过 · 经 TUN 连接 %s 用时 %d ms", required, target, tcp.Milliseconds())
	}
	return fmt.Sprintf("%d 项全部通过", required)
}
