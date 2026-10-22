package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is what the tunnel itself is doing, as the page shows it.
type State string

const (
	StateIdle     State = "idle"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateError    State = "error"
)

// A failed sample retries at this cadence instead of waiting out a long
// user-configured interval.
const probeRetryInterval = 5 * time.Second

// Status is the whole live picture of a session.
type Status struct {
	State                State   `json:"state"`
	Message              string  `json:"message"`
	Mode                 string  `json:"mode"`
	RouteMode            string  `json:"routeMode"`
	NodeID               string  `json:"nodeId"`
	NodeName             string  `json:"nodeName"`
	Server               string  `json:"server"`
	ServerIP             string  `json:"serverIp"`
	Listen               string  `json:"listen"`
	Target               string  `json:"target"`
	Forwards             int     `json:"forwards"`
	RTTms                int64   `json:"rttMs"`
	UptimeSec            int64   `json:"uptimeSec"`
	SendBps              float64 `json:"sendBps"`
	RecvBps              float64 `json:"recvBps"`
	Streams              int     `json:"streams"`
	SendTotalB           int64   `json:"sendTotalB"`
	RecvTotalB           int64   `json:"recvTotalB"`
	History              []int64 `json:"history"`
	StartedAt            string  `json:"startedAt"`
	ProbeAge             int64   `json:"probeAge"`
	Pid                  int     `json:"pid"`
	ProxyAddr            string  `json:"proxyAddr"`
	SystemProxy          bool    `json:"systemProxy"`
	SystemProxyActive    bool    `json:"systemProxyActive"`
	SystemProxySupported bool    `json:"systemProxySupported"`
	ProxyReady           bool    `json:"proxyReady"`
	TunEnabled           bool    `json:"tunEnabled"` // user's preference, survives idle/stop
	TunActive            bool    `json:"tunActive"`  // default routes are installed now
	TunMessage           string  `json:"tunMessage"` // state or last setup error
	Admin                bool    `json:"admin"`
	TunSupported         bool    `json:"tunSupported"`
	Sampling             bool    `json:"sampling"`
	LatencyMode          string  `json:"latencyMode"`
	ProbeTarget          string  `json:"probeTarget"`
	ProbeInterval        int     `json:"probeInterval"`
	ProbeError           string  `json:"probeError"`
	KeyLen               int     `json:"keyLen"`
}

// Info describes the server app itself, and the client executable it drives.
type Info struct {
	Version              string `json:"version"`
	Platform             string `json:"platform"`
	OS                   string `json:"os"`
	DataDir              string `json:"dataDir"`
	DBPath               string `json:"dbPath"`
	SessionPath          string `json:"sessionPath"`
	ClientPath           string `json:"clientPath"`
	SystemProxySupported bool   `json:"systemProxySupported"`
	WebHost              string `json:"webHost"` // 工作台当前的监听范围
	WebPort              string `json:"webPort"`
	LanIP                string `json:"lanIp"` // 本机局域网地址（展示用）
	TokenRequired        bool   `json:"tokenRequired"`
}

// Tunnel runs the rdp2tcp client as a child process and reads its output.
type Tunnel struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	state      State
	message    string
	profile    Profile
	settings   Settings
	loaded     bool
	started    time.Time
	history    []int64
	lastRTT    time.Time
	probeSince time.Time
	rtt        int64
	probeErr   string
	lastTunnel time.Time
	sendBps    float64
	recvBps    float64
	streams    int
	totalUp    int64
	totalDn    int64
	prevUp     int64
	prevDn     int64
	prevStat   time.Time
	serverIP   string
	pid        int
	logs       *logSink
	logsSent   int64 // last log seq a snapshot already handed out
	store      *Store
	hub        *hub
	stop       chan struct{}
	probeNow   chan struct{}
	bridge     *proxyBridge
	sysProxy   bool // applied to the OS right now
	sysWanted  bool // user's preference, survives idle/stop
	proxyWait  bool // a goroutine is waiting for the tunnel to come up
	sysSnap    systemProxySnapshot
	fwPlaced   bool // this session placed a firewall rule (guarded by mu)

	tunMu   sync.Mutex // serializes start/stop/self-test
	tunWait bool       // a goroutine is waiting to install TUN routes
	tunOn   bool       // default routes installed (guarded by mu)
	tunMsg  string     // TUN state or last failure (guarded by mu)
	tun     *tunSession

	dataDir string
	geo     *geoCache
}

var (
	reStat = regexp.MustCompile(`\[stat\] up=(\d+) down=(\d+) streams=(\d+)`)
	reANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

func NewTunnel(store *Store, logs *logSink, events *hub, dataDir string) *Tunnel {
	return &Tunnel{
		state:    StateIdle,
		settings: DefaultSettings(),
		logs:     logs,
		store:    store,
		hub:      events,
		probeNow: make(chan struct{}, 1),
		dataDir:  dataDir,
		geo:      newGeoCache(dataDir),
	}
}

func platformName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return "Linux"
	}
}

var goos = runtime.GOOS

// Info answers the page's first questions: where are things, which client was
// found. It must not run the executable.
func (t *Tunnel) Info(webHost, webPort string) Info {
	path, err := findBinary(t.Settings().BinaryPath)
	if err != nil {
		path = ""
	}
	return Info{
		Version:              appVersion,
		Platform:             platformName(),
		OS:                   goos,
		DataDir:              t.dataDir,
		DBPath:               t.store.Path(),
		SessionPath:          t.sessionPath(),
		ClientPath:           path,
		SystemProxySupported: systemProxySupported(),
		WebHost:              webHost,
		WebPort:              webPort,
		LanIP:                lanIPv4(),
	}
}

// lanIPv4 picks one up IPv4 address of a live, non-loopback interface, for
// showing the LAN users where to point their browser.
func lanIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if v4 := ipn.IP.To4(); v4 != nil {
					return v4.String()
				}
			}
		}
	}
	return ""
}

func (t *Tunnel) sessionPath() string {
	return filepath.Join(t.dataDir, "session.txt")
}

// Settings returns what is saved, loading it once.
func (t *Tunnel) Settings() Settings {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		t.settings = LoadSettings(t.store)
		t.loaded = true
		t.sysWanted = t.settings.SystemProxy && systemProxySupported()
	}
	return t.settings
}

// SaveSettings stores the form without starting anything.
func (t *Tunnel) SaveSettings(s Settings) (Settings, error) {
	return t.saveSettings(s)
}

// SetLatency stores only the latency preference.
func (t *Tunnel) SetLatency(mode, target string, interval int) (Settings, error) {
	s := t.Settings()
	s.LatencyMode = mode
	s.ProbeTarget = target
	if interval > 0 {
		s.ProbeInterval = interval
	}
	return t.saveSettings(s)
}

func (t *Tunnel) saveSettings(s Settings) (Settings, error) {
	s = s.Normalize()
	if err := s.Save(t.store); err != nil {
		return s, err
	}
	t.mu.Lock()
	latencyChanged := t.settings.LatencyMode != s.LatencyMode || t.settings.ProbeTarget != s.ProbeTarget
	intervalChanged := t.settings.ProbeInterval != s.ProbeInterval
	t.settings = s
	t.loaded = true
	t.sysWanted = s.SystemProxy && systemProxySupported()
	if latencyChanged && t.state == StateRunning {
		t.rtt = 0
		t.history = nil
		t.lastRTT = time.Time{}
		t.probeSince = time.Now()
		t.probeErr = ""
		t.lastTunnel = time.Time{}
	}
	t.mu.Unlock()
	t.logs.setFilter(s.LogLevel, s.NoLog)
	if latencyChanged || intervalChanged {
		t.logs.setQuietTarget(dataPathTarget(s))
		t.wakeProbe()
		t.emitStatus()
	}
	return s, nil
}

func (t *Tunnel) wakeProbe() {
	select {
	case t.probeNow <- struct{}{}:
	default:
	}
}

// Status returns the current session state.
func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.statusLocked()
}

func (t *Tunnel) statusLocked() Status {
	st := Status{
		State:       t.state,
		Message:     t.message,
		Mode:        t.profile.Mode,
		RouteMode:   t.profile.RouteMode,
		NodeID:      t.profile.Node.ID,
		NodeName:    t.profile.Node.Name,
		Server:      t.profile.Server,
		ServerIP:    t.serverIP,
		Listen:      t.profile.Listen,
		Target:      t.profile.Target,
		Forwards:    len(t.profile.Forwards) + len(t.profile.UDPForwards),
		History:     append([]int64{}, t.history...),
		RTTms:       t.rtt,
		SendBps:     t.sendBps,
		RecvBps:     t.recvBps,
		Streams:     t.streams,
		SendTotalB:  t.totalUp,
		RecvTotalB:  t.totalDn,
		Pid:         t.pid,
		ProbeTarget: t.profile.ProbeTarget,
		ProbeError:  t.probeErr,
		KeyLen:      len(t.profile.Key),
	}
	st.LatencyMode = normalizeLatencyMode(t.settings.LatencyMode)
	st.ProbeTarget = normalizeProbeTarget(t.settings.ProbeTarget, st.LatencyMode)
	st.ProbeInterval = normalizeProbeInterval(t.settings.ProbeInterval)
	if t.state == StateIdle {
		if n, ok := t.settings.Active(); ok {
			st.NodeID, st.NodeName, st.RouteMode = n.ID, n.Name, n.RouteMode
			st.Server = n.Server
			st.KeyLen = len(n.Key)
			st.Listen = net.JoinHostPort(t.settings.ListenHost, t.settings.ListenPort)
			st.Target = t.settings.Target
		}
	}
	if st.Mode == "" {
		st.Mode = "socks5"
	}
	if st.RouteMode == "" {
		st.RouteMode = RouteGlobal
	}
	if st.History == nil {
		st.History = []int64{}
	}
	if t.bridge != nil {
		st.ProxyAddr = t.bridge.Addr()
	}
	st.Sampling = t.state == StateRunning && (st.LatencyMode != LatencyTunnel || st.Mode == "socks5")
	st.SystemProxy = t.sysWanted
	st.SystemProxyActive = t.sysProxy
	st.SystemProxySupported = systemProxySupported()
	st.ProxyReady = t.readyLocked()
	st.TunEnabled = t.settings.TunEnabled
	st.TunActive = t.tunOn
	st.TunMessage = t.tunMsg
	st.Admin = isElevated()
	st.TunSupported = tunPlatformSupported()
	if t.state == StateRunning && !t.started.IsZero() {
		st.UptimeSec = int64(time.Since(t.started).Seconds())
		st.StartedAt = t.started.Format(time.RFC3339)
		ref := t.lastRTT
		if ref.IsZero() {
			ref = t.probeSince
		}
		if ref.IsZero() {
			st.ProbeAge = st.UptimeSec
		} else {
			st.ProbeAge = int64(time.Since(ref).Seconds())
		}
	}
	return st
}

// Start launches the client with the given settings.
func (t *Tunnel) Start(s Settings) (Status, error) {
	p, err := s.Validate()
	if err != nil {
		return t.Status(), err
	}
	s = s.Normalize()

	t.mu.Lock()
	if t.cmd != nil {
		t.mu.Unlock()
		return t.Status(), errors.New("隧道已在运行，请先停止")
	}
	t.settings = s
	t.loaded = true
	t.sysWanted = s.SystemProxy && systemProxySupported()
	t.profile = p
	t.state = StateStarting
	t.message = ""
	t.history = nil
	t.rtt = 0
	t.probeErr = ""
	t.lastRTT = time.Time{}
	t.probeSince = time.Now()
	t.lastTunnel = time.Time{}
	t.sendBps, t.recvBps, t.streams = 0, 0, 0
	t.totalUp, t.totalDn, t.prevUp, t.prevDn = 0, 0, 0, 0
	t.prevStat = time.Time{}
	t.serverIP = ""
	t.tunMsg = ""
	t.mu.Unlock()
	t.logs.setFilter(s.LogLevel, s.NoLog)
	t.logs.setQuietTarget(dataPathTarget(s))
	t.emitStatus()

	bin := loadBinary(s.BinaryPath)
	if bin.err != nil {
		t.fail(bin.err.Error())
		return t.Status(), bin.err
	}

	// A client killed from the outside leaves its child holding the port; take
	// the port back when the holder is our own binary, and explain when not.
	if pid := portOwnerPID(s.ListenPort); pid != 0 {
		holder := processImagePath(pid)
		if sameExecutable(holder, bin.path) {
			if err := killProcess(pid); err == nil {
				t.logs.debugf("已清理上次残留的客户端进程（PID %d）", pid)
				time.Sleep(300 * time.Millisecond)
			}
		} else {
			name := filepath.Base(strings.TrimSpace(holder))
			if name == "." || name == "" {
				name = "未知程序"
			}
			msg := fmt.Sprintf("本地端口 %s 已被 %s（PID %d）占用：结束它，或换一个本地端口", s.ListenPort, name, pid)
			t.fail(msg)
			return t.Status(), errors.New(msg)
		}
	}

	// Online lists are fetched here, with a bounded timeout and a bundled
	// fallback.
	if p.RouteMode == RouteRule {
		p.Node.RuleIP, p.Node.RuleDomain = prepareRuleSources(
			p.Node, filepath.Join(t.dataDir, "rules"), t.logs.debugf)
	}

	args := bin.args(p, s)
	if err := writeSessionFile(t.sessionPath(), commandLine(bin.path, args)); err != nil {
		t.logs.debugf("无法写入会话命令行：%v", err)
	}

	cmd := exec.Command(bin.path, args...)
	cmd.SysProcAttr = hiddenProcAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.fail(err.Error())
		return t.Status(), err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.fail(err.Error())
		return t.Status(), err
	}
	if err := cmd.Start(); err != nil {
		msg := fmt.Sprintf("启动 %s 失败：%v", filepath.Base(bin.path), err)
		t.fail(msg)
		return t.Status(), errors.New(msg)
	}

	stop := make(chan struct{})
	t.mu.Lock()
	t.cmd = cmd
	t.state = StateRunning
	t.started = time.Now()
	t.pid = cmd.Process.Pid
	t.stop = stop
	t.mu.Unlock()

	t.logs.debugf("客户端进程已启动：%s（pid %d，%d 个可用参数）", filepath.Base(bin.path), cmd.Process.Pid, len(bin.flags))
	if !bin.supports("stat") {
		t.logs.debugf("这个客户端没有 -stat：上行/下行与连接数将保持为 0")
	}
	t.logs.debugf("会话命令行：%s", shortPath(t.sessionPath()))
	t.emitStatus()

	// Windows 默认 BlockInbound：没有放行规则时，别的机器发来的连接被悄悄丢
	// 弃，本机回环却一切正常。规则跟随每次启动重建，失败只记日志，不拦启动。
	// 非管理员会弹一次 UAC；规则绑定客户端程序与端口，比系统弹窗生成的更严格。
	if s.ListenHost == "0.0.0.0" {
		if err := firewallAllow(FirewallRuleTunnel, bin.path, s.ListenPort); err != nil {
			t.logs.debugf("防火墙放行未成功：%v", err)
		} else {
			t.mu.Lock()
			t.fwPlaced = true
			t.mu.Unlock()
			t.logs.debugf("已在 Windows 防火墙放行 TCP %s（规则 %s，仅限客户端程序）", s.ListenPort, FirewallRuleTunnel)
		}
	} else if t.takeFwPlaced() {
		// 上次以局域网共享运行留下了规则，这次改成仅本机：顺手撤掉。
		firewallRemove(FirewallRuleTunnel)
		t.logs.debugf("已撤销防火墙放行")
	}

	go t.readLoop(stdout, stop)
	go t.readLoop(stderr, stop)
	go t.waitLoop(cmd, stop)
	go t.pumpLoop(stop)
	go t.probeLoop(stop)
	go t.resolveServer(p.Server)

	_ = t.settings.Save(t.store) // keep persisted switches in step

	if p.Mode == "socks5" {
		if bridge, err := startBridge(net.JoinHostPort("127.0.0.1", s.HTTPPort), p.DialAddr()); err == nil {
			t.mu.Lock()
			t.bridge = bridge
			t.mu.Unlock()
			t.logs.debugf("本地 HTTP 代理：http://%s → socks5 %s", bridge.Addr(), p.Listen)
			if t.sysWanted {
				t.startProxyWaiter(stop)
			}
		} else {
			t.logs.debugf("本地 HTTP 代理启动失败：%v", err)
		}
		if t.Settings().TunEnabled {
			// TUN also needs the local socks5 port; it installs its own waiter
			// that first proves the tunnel can carry data.
			t.startTunWaiter(stop)
		}
	}
	return t.Status(), nil
}

// resolveServer fills in the address the server name points at.
func (t *Tunnel) resolveServer(server string) {
	host, _, ok := splitHostPort(server)
	if !ok {
		return
	}
	done := make(chan string, 1)
	go func() {
		if addrs, err := net.LookupHost(host); err == nil && len(addrs) > 0 {
			done <- addrs[0]
			return
		}
		done <- ""
	}()
	select {
	case ip := <-done:
		t.mu.Lock()
		if t.profile.Server == server {
			t.serverIP = ip
		}
		t.mu.Unlock()
		t.emitStatus()
	case <-time.After(3 * time.Second):
	}
}

func (t *Tunnel) readyLocked() bool {
	return t.state == StateRunning && !t.lastTunnel.IsZero() && time.Since(t.lastTunnel) < 15*time.Second
}

// SetSystemProxy records the user's preference (Windows only; elsewhere the
// switch is refused before it lies).
func (t *Tunnel) SetSystemProxy(on bool) (Status, error) {
	if on && !systemProxySupported() {
		return t.Status(), errors.New("系统代理设置目前只做在 Windows 上")
	}
	t.mu.Lock()
	settings := t.settings
	settings.SystemProxy = on
	t.settings = settings
	t.loaded = true
	t.sysWanted = on
	running := t.state == StateRunning
	applied := t.sysProxy
	stop := t.stop
	bridge := t.bridge
	t.mu.Unlock()
	_ = settings.Save(t.store)

	if !on {
		if applied {
			if err := clearSystemProxy(&t.sysSnap); err != nil {
				return t.Status(), err
			}
			t.mu.Lock()
			t.sysProxy = false
			t.mu.Unlock()
			t.logs.debugf("系统代理已还原")
		} else {
			t.logs.debugf("系统代理预设已取消")
		}
		t.emitStatus()
		return t.Status(), nil
	}

	if !running || bridge == nil {
		t.logs.debugf("系统代理已预设：隧道连通后自动启用")
		t.emitStatus()
		return t.Status(), nil
	}
	if err := t.applySystemProxy(); err == nil {
		t.emitStatus()
		return t.Status(), nil
	}
	t.startProxyWaiter(stop)
	t.logs.debugf("系统代理已预设：隧道就绪后自动启用")
	t.emitStatus()
	return t.Status(), nil
}

func (t *Tunnel) applySystemProxy() error {
	t.mu.Lock()
	bridge := t.bridge
	profile := t.profile
	settings := t.settings
	applied := t.sysProxy
	t.mu.Unlock()
	if applied {
		return nil
	}
	if bridge == nil {
		return errors.New("本地 HTTP 代理还没就绪")
	}
	target := dataPathTarget(settings)
	if _, err := probeDataPath(profile.DialAddr(), target, 6*time.Second); err != nil {
		return fmt.Errorf("隧道还没能承载数据: %w", err)
	}
	if err := setSystemProxy(bridge.Addr(), &t.sysSnap); err != nil {
		return err
	}
	t.mu.Lock()
	t.sysProxy = true
	t.lastTunnel = time.Now()
	t.mu.Unlock()
	t.logs.debugf("系统代理已指向 http://%s", bridge.Addr())
	return nil
}

func (t *Tunnel) startProxyWaiter(stop chan struct{}) {
	if stop == nil {
		return
	}
	t.mu.Lock()
	if t.proxyWait {
		t.mu.Unlock()
		return
	}
	t.proxyWait = true
	t.mu.Unlock()

	go func() {
		defer func() {
			t.mu.Lock()
			t.proxyWait = false
			t.mu.Unlock()
		}()
		tick := time.NewTicker(800 * time.Millisecond)
		defer tick.Stop()
		var lastLog time.Time
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			t.mu.Lock()
			wanted, applied := t.sysWanted, t.sysProxy
			t.mu.Unlock()
			if !wanted || applied {
				return
			}
			if err := t.applySystemProxy(); err == nil {
				t.emitStatus()
				return
			} else if time.Since(lastLog) > 20*time.Second {
				t.logs.debugf("系统代理等待隧道就绪：%v", err)
				lastLog = time.Now()
			}
		}
	}()
}

// SelectNode makes id the active node; a running session restarts.
func (t *Tunnel) SelectNode(id string) (Status, error) {
	s := t.Settings().Normalize()
	if _, ok := s.nodeByID(id); !ok {
		return t.Status(), errors.New("节点不存在")
	}
	s.ActiveNode = id
	if _, err := t.SaveSettings(s); err != nil {
		return t.Status(), err
	}
	if !t.sessionLive() {
		t.emitStatus()
		return t.Status(), nil
	}
	return t.restartSession(s)
}

// SetNodeRoute changes a node's split-tunnel choice.
func (t *Tunnel) SetNodeRoute(id, routeMode, ruleBasis string) (Status, error) {
	s := t.Settings().Normalize()
	index := -1
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return t.Status(), errors.New("节点不存在")
	}
	s.Nodes[index].RouteMode = normalizeRouteMode(routeMode)
	s.Nodes[index].RuleBasis = normalizeRuleBasis(ruleBasis)
	if s.Nodes[index].RouteMode != RouteRule {
		s.Nodes[index].RuleBasis = RuleBasisList
	}
	s = s.Normalize()
	if s.ActiveNode == id {
		if _, err := s.Validate(); err != nil {
			return t.Status(), err
		}
	}
	if _, err := t.SaveSettings(s); err != nil {
		return t.Status(), err
	}
	if s.ActiveNode != id || !t.sessionLive() {
		t.emitStatus()
		return t.Status(), nil
	}
	return t.restartSession(s)
}

func (t *Tunnel) sessionLive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cmd != nil && (t.state == StateRunning || t.state == StateStarting)
}

func (t *Tunnel) restartSession(s Settings) (Status, error) {
	if _, err := t.Stop(); err != nil {
		return t.Status(), err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		t.mu.Lock()
		cmd := t.cmd
		t.mu.Unlock()
		if cmd == nil {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	return t.Start(s)
}

// Stop kills the client process and waits for it.
func (t *Tunnel) Stop() (Status, error) {
	t.mu.Lock()
	cmd := t.cmd
	if cmd == nil {
		t.mu.Unlock()
		return t.Status(), nil
	}
	t.state = StateStopping
	stop := t.stop
	t.stop = nil
	t.mu.Unlock()
	t.emitStatus()
	if stop != nil {
		close(stop)
	}
	t.logs.debugf("正在停止…")
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return t.Status(), nil
}

func (t *Tunnel) waitLoop(cmd *exec.Cmd, stop chan struct{}) {
	err := cmd.Wait()
	select {
	case <-stop:
	default:
	}

	t.mu.Lock()
	wasStopping := t.state == StateStopping
	t.cmd = nil
	t.pid = 0
	// Claim the firewall rule here, where the session slot is released: a fast
	// restart starts a new session the moment cmd is nil, and its fresh rule
	// must not be the one this cleanup tears down.
	fwPlaced := t.fwPlaced
	t.fwPlaced = false
	if wasStopping {
		t.state = StateIdle
		t.message = ""
	} else {
		t.state = StateError
		reason := t.logs.lastError()
		switch {
		case reason != "":
			t.message = "客户端已退出：" + truncate(reason, 160)
		case err != nil:
			t.message = "客户端已退出：" + err.Error()
		default:
			t.message = "客户端已退出"
		}
	}
	t.sendBps, t.recvBps, t.streams = 0, 0, 0
	t.started = time.Time{}
	t.rtt = 0
	t.history = nil
	t.lastRTT = time.Time{}
	t.probeSince = time.Time{}
	t.lastTunnel = time.Time{}
	msg := t.message
	t.mu.Unlock()

	if wasStopping {
		t.logs.debugf("隧道已停止")
	} else if msg != "" {
		t.logs.debugf("%s", msg)
	}

	// A dead tunnel cannot carry TUN traffic: remove its default routes now.
	t.stopTun()

	t.mu.Lock()
	proxyWasOn := t.sysProxy
	bridge := t.bridge
	t.sysProxy = false
	t.bridge = nil
	t.mu.Unlock()
	if proxyWasOn {
		_ = clearSystemProxy(&t.sysSnap)
		t.logs.debugf("系统代理已还原")
	}
	if bridge != nil {
		bridge.Close()
	}
	if fwPlaced {
		firewallRemove(FirewallRuleTunnel)
		t.logs.debugf("已撤销防火墙放行")
	}
	t.emitStatus()
}

// takeFwPlaced reports whether this app still owns a firewall rule from an
// earlier session, clearing the flag as it goes.
func (t *Tunnel) takeFwPlaced() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	placed := t.fwPlaced
	t.fwPlaced = false
	return placed
}

// Shutdown runs when the server itself is going away: waitLoop may not get a
// turn, so the rules are removed unconditionally — deleting a rule that is not
// there is a harmless no-op.
func (t *Tunnel) Shutdown() {
	// Routes must be gone before the process disappears, otherwise the machine
	// would keep sending all IPv4 traffic into a tunnel that no longer exists.
	t.stopTun()
	_, _ = t.Stop()
	firewallRemove(FirewallRuleTunnel)
	firewallRemove(FirewallRuleUI)
}

// pumpLoop pushes new log lines to the hub every 300ms, and the dashboard once
// a second so uptime and "no answer yet" stay live.
func (t *Tunnel) pumpLoop(stop chan struct{}) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	t.mu.Lock()
	last := t.logsSent
	t.mu.Unlock()
	n := 0
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			t.mu.Lock()
			if t.logsSent > last {
				last = t.logsSent
			}
			t.mu.Unlock()
			lines := t.logs.since(last)
			if len(lines) > 0 {
				if len(lines) > 200 {
					lines = lines[len(lines)-200:]
					t.hub.emit("logs", LogBatch{Lines: lines, Reset: true})
				} else {
					t.hub.emit("logs", LogBatch{Lines: lines})
				}
				last = lines[len(lines)-1].Seq
				t.mu.Lock()
				if last > t.logsSent {
					t.logsSent = last
				}
				t.mu.Unlock()
			}
			n++
			if n%3 == 0 {
				t.emitStatus()
			}
		}
	}
}

func (t *Tunnel) readLoop(r io.Reader, stop chan struct{}) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		select {
		case <-stop:
			return
		default:
		}
		line := reANSI.ReplaceAllString(scanner.Text(), "")
		if strings.TrimSpace(line) == "" {
			continue
		}
		t.handleLine(line)
	}
}

func (t *Tunnel) handleLine(line string) {
	if m := reStat.FindStringSubmatch(line); m != nil {
		up, _ := strconv.ParseInt(m[1], 10, 64)
		down, _ := strconv.ParseInt(m[2], 10, 64)
		streams, _ := strconv.Atoi(m[3])
		t.applyStats(up, down, streams)
		t.logs.WriteLine(line)
		return
	}
	t.logs.WriteLine(line)
}

func (t *Tunnel) applyStats(up, down int64, streams int) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if up < t.prevUp || down < t.prevDn {
		t.prevUp, t.prevDn, t.prevStat = 0, 0, time.Time{}
	}
	t.totalUp, t.totalDn = up, down
	if !t.prevStat.IsZero() {
		if elapsed := now.Sub(t.prevStat).Seconds(); elapsed > 0.2 {
			t.sendBps = float64(up-t.prevUp) / elapsed
			t.recvBps = float64(down-t.prevDn) / elapsed
		}
	}
	t.prevUp, t.prevDn, t.prevStat = up, down, now
	t.streams = streams
}

// probeLoop samples once immediately, then waits the configured interval.
func (t *Tunnel) probeLoop(stop chan struct{}) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		ok := t.sampleLatency()

		settings := t.Settings()
		interval := time.Duration(normalizeProbeInterval(settings.ProbeInterval)) * time.Second
		if !ok && interval > probeRetryInterval {
			interval = probeRetryInterval
		}
		timer.Reset(interval)
		select {
		case <-stop:
			return
		case <-t.probeNow:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (t *Tunnel) sampleLatency() bool {
	t.mu.Lock()
	profile := t.profile
	state := t.state
	settings := t.settings
	t.mu.Unlock()
	if state != StateRunning {
		return false
	}
	mode := settings.LatencyMode
	target := settings.ProbeTarget

	var d time.Duration
	var err error
	switch mode {
	case LatencyTCP:
		d, err = measureTCP(target, 5*time.Second)
	case LatencyPing:
		d, err = measurePing(target, 3*time.Second)
	default:
		if profile.Mode != "socks5" {
			return false
		}
		d, err = probeDataPath(profile.DialAddr(), dataPathTarget(settings), 5*time.Second)
	}

	now := time.Now()
	t.mu.Lock()
	if t.state != StateRunning || t.settings.LatencyMode != mode || t.settings.ProbeTarget != target {
		t.mu.Unlock()
		return false
	}
	if err != nil {
		t.rtt = 0
		t.probeErr = truncate(err.Error(), 200)
		t.mu.Unlock()
		return false
	}
	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	t.probeErr = ""
	t.rtt = ms
	t.lastRTT = now
	t.history = append(t.history, ms)
	if len(t.history) > 90 {
		t.history = t.history[len(t.history)-90:]
	}
	if mode == LatencyTunnel {
		t.lastTunnel = now
	}
	t.mu.Unlock()
	return true
}

func (t *Tunnel) fail(msg string) {
	t.mu.Lock()
	t.cmd = nil
	t.pid = 0
	t.state = StateError
	t.message = msg
	t.mu.Unlock()
	t.logs.debugf("%s", msg)
	t.emitStatus()
}

func (t *Tunnel) emitStatus() {
	t.mu.Lock()
	st := t.statusLocked()
	lines := t.logs.since(t.logsSent)
	if len(lines) > 0 {
		t.logsSent = lines[len(lines)-1].Seq
	}
	t.mu.Unlock()
	t.hub.emit("status", st)
	// Failures logged before the session's pump loop exists (bad binary, a
	// taken port) must still reach the page; flushing here covers them.
	if len(lines) > 0 {
		if len(lines) > 200 {
			lines = lines[len(lines)-200:]
			t.hub.emit("logs", LogBatch{Lines: lines, Reset: true})
		} else {
			t.hub.emit("logs", LogBatch{Lines: lines})
		}
	}
}

// Logs returns the session log so far, for a page that just loaded.
func (t *Tunnel) Logs() []LogLine {
	lines := t.logs.snapshot()
	t.mu.Lock()
	if seq := t.logs.lastSeq(); seq > t.logsSent {
		t.logsSent = seq
	}
	t.mu.Unlock()
	return lines
}

func (t *Tunnel) ClearLogs() {
	t.logs.clear()
	_ = t.store.ClearLogs()
}

// Probe tests the server the way the client does.
func ProbeServer(server string) (ProbeResult, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return ProbeResult{}, errors.New("请先填写服务器地址")
	}
	return Probe(server, 8*time.Second)
}

// Locate answers where a node's server lives, for the flag on its row.
func (t *Tunnel) Locate(server string) (GeoInfo, error) {
	return t.geo.Locate(server)
}

// sameExecutable answers "is this the very binary we launch".
func sameExecutable(a, b string) bool {
	a = strings.ToLower(strings.Trim(strings.TrimSpace(a), `"`))
	b = strings.ToLower(strings.Trim(strings.TrimSpace(b), `"`))
	return a != "" && b != "" && a == b
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
