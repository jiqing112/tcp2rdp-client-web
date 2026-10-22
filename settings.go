package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Split-tunnel modes understood by rdp2tcp -mode.
const (
	RouteGlobal = "global"
	RouteRule   = "rule"
	RouteDirect = "direct"
)

// Rule source markers. A source is either one of these, or a file path / URL
// that replaces the default list.
const (
	RuleBuiltin = "builtin"
	RuleRemote  = "remote"
	RuleOff     = "off"

	// How a rule node decides a destination. "list" uses the domestic IP and
	// domain lists (the default); "ip" ignores the domain list and lets the
	// resolved IP decide, so every foreign IP goes through the tunnel even when
	// its domain is on a domestic list.
	RuleBasisList = "list"
	RuleBasisIP   = "ip"

	DefaultRuleDNS = "1.1.1.1:53"
)

// Latency modes the dashboard can plot.
const (
	LatencyTunnel = "tunnel"
	LatencyTCP    = "tcp"
	LatencyPing   = "ping"
)

const (
	DefaultProbeInterval = 1800 // seconds
	MaxProbeInterval     = 86400
)

// Node is one server the app can connect through. Routing lives on the node:
// a direct node needs neither an address nor a key.
type Node struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Server     string `json:"server"`
	Key        string `json:"key"`
	RouteMode  string `json:"routeMode"`
	RuleBasis  string `json:"ruleBasis"`
	RuleIP     string `json:"ruleIp"`
	RuleDomain string `json:"ruleDomain"`
	RuleDNS    string `json:"ruleDns"`
}

// Settings is everything the page lets the user change. Numbers stay strings so
// that a half-typed field never jumps back to a default while the user is
// editing it.
type Settings struct {
	Nodes      []Node `json:"nodes"`
	ActiveNode string `json:"activeNode"`

	ListenHost    string `json:"listenHost"`
	ListenPort    string `json:"listenPort"`
	Socks5        bool   `json:"socks5"`
	Target        string `json:"target"`
	Forwards      string `json:"forwards"`
	UDPForwards   string `json:"udpForwards"`
	Timeout       string `json:"timeout"`
	TLSFP         string `json:"tlsFp"`
	Pad           string `json:"pad"`
	PadMax        string `json:"padMax"`
	Tick          string `json:"tick"`
	Pin           string `json:"pin"`
	KnownHosts    string `json:"knownHosts"`
	Insecure      bool   `json:"insecure"`
	NoLog         bool   `json:"noLog"`
	LogLevel      string `json:"logLevel"`
	BinaryPath    string `json:"binaryPath"`
	ProbeTarget   string `json:"probeTarget"`
	LatencyMode   string `json:"latencyMode"`
	ProbeInterval int    `json:"probeInterval"`
	SystemProxy   bool   `json:"systemProxy"`
	TunEnabled    bool   `json:"tunEnabled"` // 预设：连通后接管整机 IPv4 流量
	HTTPPort      string `json:"httpProxyPort"`
}

func DefaultSettings() Settings {
	return Settings{
		ListenHost:    "127.0.0.1",
		ListenPort:    "1080",
		Socks5:        true,
		Timeout:       "10",
		TLSFP:         "windows",
		Pad:           "block",
		PadMax:        "512",
		Tick:          "0",
		LogLevel:      "info",
		ProbeTarget:   "www.baidu.com:443",
		LatencyMode:   LatencyTunnel,
		ProbeInterval: DefaultProbeInterval,
		HTTPPort:      "34457",
	}
}

// Profile is the resolved, ready-to-use form of Settings.
type Profile struct {
	Node        Node     `json:"node"`
	Server      string   `json:"server"`
	Listen      string   `json:"listen"`
	Mode        string   `json:"mode"`
	RouteMode   string   `json:"routeMode"`
	Target      string   `json:"target"`
	Forwards    []string `json:"forwards"`
	UDPForwards []string `json:"udpForwards"`
	Timeout     int      `json:"timeout"`
	Key         string   `json:"key"`
	TLSFP       string   `json:"tlsFp"`
	Pad         string   `json:"pad"`
	PadMax      int      `json:"padMax"`
	TickMs      int      `json:"tickMs"`
	Pin         string   `json:"pin"`
	KnownHosts  string   `json:"knownHosts"`
	Insecure    bool     `json:"insecure"`
	ProbeTarget string   `json:"probeTarget"`
	Summary     string   `json:"summary"`
}

// DialAddr is the address this machine uses to reach the tunnel's own port.
func (p Profile) DialAddr() string {
	host, port, ok := splitHostPort(p.Listen)
	if !ok {
		return p.Listen
	}
	switch host {
	case "", "0.0.0.0", "::":
		return net.JoinHostPort("127.0.0.1", port)
	}
	return p.Listen
}

func isPort(v string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	return err == nil && n > 0 && n < 65536
}

func splitHostPort(v string) (string, string, bool) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(v))
	if err != nil {
		return "", "", false
	}
	return host, port, host != "" && isPort(port)
}

// normalizeServer turns what people paste (a URL, a bare host, host:port) into
// the host:port rdp2tcp wants.
func normalizeServer(raw string) string {
	s := strings.TrimSpace(raw)
	for _, p := range []string{"rdp://", "tls://", "tcp://", "https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimPrefix(s, "//")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}
	if host, port, ok := splitHostPort(s); ok {
		return net.JoinHostPort(host, port)
	}
	if strings.Count(s, ":") > 1 && !strings.HasPrefix(s, "[") {
		return "[" + s + "]:3389"
	}
	if strings.Contains(s, ":") {
		return s
	}
	return s + ":3389"
}

func normalizeRouteMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case RouteRule:
		return RouteRule
	case RouteDirect:
		return RouteDirect
	default:
		return RouteGlobal
	}
}

func normalizeLatencyMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case LatencyTCP:
		return LatencyTCP
	case LatencyPing:
		return LatencyPing
	default:
		return LatencyTunnel
	}
}

func normalizeProbeInterval(n int) int {
	if n <= 0 {
		return DefaultProbeInterval
	}
	if n > MaxProbeInterval {
		return MaxProbeInterval
	}
	return n
}

func normalizeProbeTarget(raw, mode string) string {
	s := strings.TrimSpace(raw)
	if mode == LatencyPing {
		if s == "" {
			return "www.baidu.com"
		}
		if host, _, ok := splitHostPort(normalizeServer(s)); ok {
			return host
		}
		return strings.Trim(s, "[]")
	}
	if s == "" {
		return "www.baidu.com:443"
	}
	if host, port, ok := splitHostPort(s); ok {
		return net.JoinHostPort(host, port)
	}
	if strings.Count(s, ":") > 1 && !strings.HasPrefix(s, "[") {
		return "[" + s + "]:443"
	}
	if !strings.Contains(s, ":") {
		return s + ":443"
	}
	return s
}

func routeLabel(mode string) string {
	switch mode {
	case RouteRule:
		return "规则分流"
	case RouteDirect:
		return "全部直连"
	default:
		return "全局代理"
	}
}

func normalizeRuleBasis(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), RuleBasisIP) {
		return RuleBasisIP
	}
	return RuleBasisList
}

func normalizeRuleSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", RuleBuiltin:
		return RuleBuiltin
	case RuleRemote, "default":
		return RuleRemote
	case RuleOff, "none":
		return RuleOff
	default:
		return strings.TrimSpace(v)
	}
}

func normalizeRuleDNS(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "", "default":
		return DefaultRuleDNS
	case "local", RuleOff:
		return s
	}
	host, port, ok := splitHostPort(strings.TrimSpace(v))
	if !ok || host == "" || !isPort(port) {
		return DefaultRuleDNS
	}
	return strings.TrimSpace(v)
}

func defaultNodeName(n Node) string {
	if n.RouteMode == RouteDirect {
		return "直连"
	}
	if host, _, ok := splitHostPort(n.Server); ok {
		return host
	}
	if s := strings.TrimSpace(n.Server); s != "" {
		return s
	}
	return "新节点"
}

func normalizeNodes(nodes []Node) []Node {
	out := make([]Node, 0, len(nodes)+1)
	used := map[string]bool{}
	for _, n := range nodes {
		n.ID = strings.TrimSpace(n.ID)
		n.Name = strings.TrimSpace(n.Name)
		n.Server = normalizeServer(n.Server)
		n.Key = strings.TrimSpace(n.Key)
		n.RouteMode = normalizeRouteMode(n.RouteMode)
		n.RuleBasis = normalizeRuleBasis(n.RuleBasis)
		n.RuleIP = normalizeRuleSource(n.RuleIP)
		n.RuleDomain = normalizeRuleSource(n.RuleDomain)
		n.RuleDNS = normalizeRuleDNS(n.RuleDNS)
		if n.RouteMode == RouteRule && n.RuleBasis == RuleBasisIP {
			n.RuleDomain = RuleOff
			if n.RuleDNS == RuleOff {
				n.RuleDNS = DefaultRuleDNS
			}
		}
		if n.ID == "" {
			n.ID = fmt.Sprintf("node-%d", len(out)+1)
		}
		base := n.ID
		for i := 2; used[n.ID]; i++ {
			n.ID = fmt.Sprintf("%s-%d", base, i)
		}
		if n.Name == "" {
			n.Name = defaultNodeName(n)
		}
		used[n.ID] = true
		out = append(out, n)
	}
	return out
}

func (s Settings) nodeByID(id string) (Node, bool) {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Active returns the node the next session will use.
func (s Settings) Active() (Node, bool) {
	if n, ok := s.nodeByID(s.ActiveNode); ok {
		return n, true
	}
	if len(s.Nodes) > 0 {
		return s.Nodes[0], true
	}
	return Node{}, false
}

// Normalize cleans up what the user typed and fills the gaps between fields.
func (s Settings) Normalize() Settings {
	switch strings.TrimSpace(s.ListenHost) {
	case "0.0.0.0", "*", "::":
		s.ListenHost = "0.0.0.0"
	default:
		s.ListenHost = "127.0.0.1"
	}
	s.ListenPort = strings.TrimPrefix(strings.TrimSpace(s.ListenPort), ":")
	s.Target = strings.TrimSpace(s.Target)
	s.Timeout = strings.TrimSpace(s.Timeout)
	s.BinaryPath = strings.TrimSpace(s.BinaryPath)
	s.KnownHosts = strings.TrimSpace(s.KnownHosts)
	s.Pin = strings.ToLower(strings.TrimSpace(s.Pin))
	s.Pin = strings.TrimPrefix(strings.TrimPrefix(s.Pin, "sha256:"), "SHA256:")
	s.Forwards = strings.TrimSpace(s.Forwards)
	s.UDPForwards = strings.TrimSpace(s.UDPForwards)

	s.Nodes = normalizeNodes(s.Nodes)
	if len(s.Nodes) == 0 {
		s.ActiveNode = ""
	} else if _, ok := s.nodeByID(s.ActiveNode); !ok {
		s.ActiveNode = s.Nodes[0].ID
	}

	switch strings.ToLower(strings.TrimSpace(s.TLSFP)) {
	case "", "windows", "win", "schannel":
		s.TLSFP = "windows"
	case "go", "golang":
		s.TLSFP = "go"
	case "chrome", "firefox", "edge":
		s.TLSFP = strings.ToLower(strings.TrimSpace(s.TLSFP))
	}
	switch strings.ToLower(strings.TrimSpace(s.Pad)) {
	case "", "off", "none":
		s.Pad = "off"
	case "block", "full", "rand":
		s.Pad = strings.ToLower(strings.TrimSpace(s.Pad))
	}
	s.PadMax = strings.TrimSpace(s.PadMax)
	if n, err := strconv.Atoi(s.PadMax); err != nil || n < 0 || n > 8192 {
		s.PadMax = "512"
	}
	s.Tick = strings.TrimSpace(s.Tick)
	if n, err := strconv.Atoi(s.Tick); err != nil || n < 0 || n > 1000 {
		s.Tick = "0"
	}

	s.LogLevel = strings.ToLower(strings.TrimSpace(s.LogLevel))
	if _, ok := levelRank[s.LogLevel]; !ok {
		s.LogLevel = "info"
	}

	s.HTTPPort = strings.TrimSpace(s.HTTPPort)
	if !isPort(s.HTTPPort) {
		s.HTTPPort = "34457"
	}

	s.LatencyMode = normalizeLatencyMode(s.LatencyMode)
	s.ProbeTarget = normalizeProbeTarget(s.ProbeTarget, s.LatencyMode)
	s.ProbeInterval = normalizeProbeInterval(s.ProbeInterval)

	if s.Socks5 {
		s.Target = ""
	}
	return s
}

// Validate turns Settings into a Profile, or says exactly which field is wrong.
func (s Settings) Validate() (Profile, error) {
	s = s.Normalize()
	node, ok := s.Active()
	if !ok {
		return Profile{}, errors.New("还没有节点：先点「新增节点」填写服务器")
	}
	if !isPort(s.ListenPort) {
		return Profile{}, fmt.Errorf("本地监听端口无效：%q，应为 1-65535", s.ListenPort)
	}
	if node.RouteMode != RouteDirect {
		if node.Server == "" {
			return Profile{}, fmt.Errorf("节点「%s」还没有填写服务器地址", node.Name)
		}
		if host, port, ok := splitHostPort(node.Server); !ok || host == "" || !isPort(port) {
			return Profile{}, fmt.Errorf("服务器地址无效：%q，应形如 1.2.3.4:3389", node.Server)
		}
		if node.Key == "" {
			return Profile{}, fmt.Errorf("请填写节点「%s」的密钥：必须与服务端 -key 一致", node.Name)
		}
	}
	if node.RouteMode == RouteRule && node.RuleBasis == RuleBasisIP && node.RuleIP == RuleOff {
		return Profile{}, fmt.Errorf("节点「%s」选择了只按 IP 判断，国内 IP 清单不能关闭", node.Name)
	}
	if node.RouteMode == RouteRule && node.RuleIP == RuleOff && node.RuleDomain == RuleOff {
		return Profile{}, fmt.Errorf("节点「%s」的国内 IP 和域名清单不能同时关闭", node.Name)
	}
	timeout, err := strconv.Atoi(s.Timeout)
	if err != nil || timeout <= 0 || timeout > 300 {
		return Profile{}, fmt.Errorf("超时时间无效：%q，应为 1-300 秒", s.Timeout)
	}

	p := Profile{
		Node:        node,
		Server:      node.Server,
		Listen:      net.JoinHostPort(s.ListenHost, s.ListenPort),
		Mode:        "forward",
		RouteMode:   node.RouteMode,
		Target:      s.Target,
		Timeout:     timeout,
		Key:         node.Key,
		TLSFP:       s.TLSFP,
		Pad:         s.Pad,
		Pin:         s.Pin,
		KnownHosts:  s.KnownHosts,
		Insecure:    s.Insecure,
		ProbeTarget: s.ProbeTarget,
	}
	if p.PadMax, err = strconv.Atoi(s.PadMax); err != nil {
		return Profile{}, fmt.Errorf("随机填充上限无效：%q", s.PadMax)
	}
	if p.TickMs, err = strconv.Atoi(s.Tick); err != nil {
		return Profile{}, fmt.Errorf("发送合并时间窗无效：%q", s.Tick)
	}

	forwards, err := parseForwards(s.Forwards, "-L")
	if err != nil {
		return Profile{}, err
	}
	p.Forwards = forwards
	udpForwards, err := parseForwards(s.UDPForwards, "-U")
	if err != nil {
		return Profile{}, err
	}
	p.UDPForwards = udpForwards

	if s.Socks5 {
		p.Mode = "socks5"
	} else {
		if s.Target == "" && len(forwards) == 0 {
			return Profile{}, errors.New("请填写转发目标，或把代理模式改成 SOCKS5")
		}
		if s.Target != "" {
			host, port, ok := splitHostPort(s.Target)
			if !ok || host == "" || !isPort(port) {
				return Profile{}, fmt.Errorf("转发目标无效：%q，应形如 127.0.0.1:8080", s.Target)
			}
			p.Target = net.JoinHostPort(host, port)
		}
	}
	p.Summary = profileSummary(p)
	return p, nil
}

// parseForwards reads the multi-line -L / -U box.
func parseForwards(text, flag string) ([]string, error) {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 3 && len(parts) != 4 {
			return nil, fmt.Errorf("%s 第 %d 行格式不对：%q，应为 [lhost:]lport:rhost:rport", flag, i+1, line)
		}
		portIdx := 0
		if len(parts) == 4 {
			portIdx = 1
		}
		if !isPort(parts[portIdx]) || !isPort(parts[len(parts)-1]) {
			return nil, fmt.Errorf("%s 第 %d 行端口无效：%q", flag, i+1, line)
		}
		out = append(out, line)
	}
	return out, nil
}

func profileSummary(p Profile) string {
	role := "SOCKS5 代理"
	switch {
	case p.Mode == "socks5":
		if n := len(p.Forwards) + len(p.UDPForwards); n > 0 {
			role = fmt.Sprintf("SOCKS5 + %d 条转发", n)
		}
	case p.Target != "":
		role = "端口转发"
	case len(p.Forwards)+len(p.UDPForwards) > 0:
		role = fmt.Sprintf("%d 条转发", len(p.Forwards)+len(p.UDPForwards))
	}
	return fmt.Sprintf("%s · %s · RDP 伪装 · %s", role, routeLabel(p.RouteMode), padLabel(p.Pad))
}

func padLabel(pad string) string {
	switch pad {
	case "off":
		return "不填充"
	case "full":
		return "定长填充"
	case "rand":
		return "随机填充"
	default:
		return "块对齐填充"
	}
}

// ---- persistence: settings minus nodes live as kv rows; nodes in their table.

var settingsJSONFields = []string{
	"listenHost", "listenPort", "socks5", "target", "forwards", "udpForwards",
	"timeout", "tlsFp", "pad", "padMax", "tick", "pin", "knownHosts", "insecure",
	"noLog", "logLevel", "binaryPath", "probeTarget", "latencyMode", "probeInterval",
	"systemProxy", "tunEnabled", "httpProxyPort",
}

// Save persists the settings (nodes included) into SQLite.
func (s Settings) Save(store *Store) error {
	s = s.Normalize()
	obj := map[string]json.RawMessage{}
	for _, f := range settingsJSONFields {
		switch f {
		case "socks5", "insecure", "noLog", "systemProxy", "tunEnabled":
			b, _ := json.Marshal(fieldBool(s, f))
			obj[f] = b
		case "probeInterval":
			b, _ := json.Marshal(s.ProbeInterval)
			obj[f] = b
		default:
			b, _ := json.Marshal(fieldString(s, f))
			obj[f] = b
		}
	}
	if err := store.SaveSettingsJSON(obj); err != nil {
		return err
	}
	return store.SaveNodes(s.Nodes, s.ActiveNode)
}

// LoadSettings reads back what Save wrote; missing rows keep their defaults.
func LoadSettings(store *Store) Settings {
	out := DefaultSettings()
	raw, err := store.LoadSettingsJSON()
	if err == nil {
		getStr := func(key string, dst *string) {
			if v, ok := raw[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					*dst = s
				}
			}
		}
		getBool := func(key string, dst *bool) {
			if v, ok := raw[key]; ok {
				var b bool
				if json.Unmarshal(v, &b) == nil {
					*dst = b
				}
			}
		}
		getStr("listenHost", &out.ListenHost)
		getStr("listenPort", &out.ListenPort)
		getBool("socks5", &out.Socks5)
		getStr("target", &out.Target)
		getStr("forwards", &out.Forwards)
		getStr("udpForwards", &out.UDPForwards)
		getStr("timeout", &out.Timeout)
		getStr("tlsFp", &out.TLSFP)
		getStr("pad", &out.Pad)
		getStr("padMax", &out.PadMax)
		getStr("tick", &out.Tick)
		getStr("pin", &out.Pin)
		getStr("knownHosts", &out.KnownHosts)
		getBool("insecure", &out.Insecure)
		getBool("noLog", &out.NoLog)
		getStr("logLevel", &out.LogLevel)
		getStr("binaryPath", &out.BinaryPath)
		getStr("probeTarget", &out.ProbeTarget)
		getStr("latencyMode", &out.LatencyMode)
		if v, ok := raw["probeInterval"]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil {
				out.ProbeInterval = n
			}
		}
		getBool("systemProxy", &out.SystemProxy)
		getBool("tunEnabled", &out.TunEnabled)
		getStr("httpProxyPort", &out.HTTPPort)
	}
	if nodes, err := store.LoadNodes(); err == nil && len(nodes) > 0 {
		out.Nodes = nodes
	}
	if v, ok := store.GetSetting("activeNode"); ok {
		out.ActiveNode = v
	}
	return out.Normalize()
}

func fieldBool(s Settings, key string) bool {
	switch key {
	case "socks5":
		return s.Socks5
	case "insecure":
		return s.Insecure
	case "noLog":
		return s.NoLog
	case "systemProxy":
		return s.SystemProxy
	case "tunEnabled":
		return s.TunEnabled
	}
	return false
}

func fieldString(s Settings, key string) string {
	switch key {
	case "listenHost":
		return s.ListenHost
	case "listenPort":
		return s.ListenPort
	case "target":
		return s.Target
	case "forwards":
		return s.Forwards
	case "udpForwards":
		return s.UDPForwards
	case "timeout":
		return s.Timeout
	case "tlsFp":
		return s.TLSFP
	case "pad":
		return s.Pad
	case "padMax":
		return s.PadMax
	case "tick":
		return s.Tick
	case "pin":
		return s.Pin
	case "knownHosts":
		return s.KnownHosts
	case "logLevel":
		return s.LogLevel
	case "binaryPath":
		return s.BinaryPath
	case "probeTarget":
		return s.ProbeTarget
	case "latencyMode":
		return s.LatencyMode
	case "httpProxyPort":
		return s.HTTPPort
	}
	return ""
}
