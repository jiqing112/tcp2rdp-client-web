package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// The web workbench does not link the tunnel either: it drives whatever
// rdp2tcp executable sits next to it (or wherever the user points), the same
// binary the server runs. Windows names it rdp2tcp.exe, everything else rdp2tcp.

func clientExeName() string {
	if runtime.GOOS == "windows" {
		return "rdp2tcp.exe"
	}
	return "rdp2tcp"
}

// Flag names may contain dashes (`-rule-ip`, `-tls-fp`, `-pad-max`).
var flagLine = regexp.MustCompile(`(?m)^\s+-([a-zA-Z0-9_-]+)`)

type clientBinary struct {
	path  string
	flags map[string]bool
	err   error
}

var (
	binMu    sync.Mutex
	binCache map[string]*clientBinary
)

// findBinary looks for the client where a user would put it.
func findBinary(custom string) (string, error) {
	custom = strings.TrimSpace(custom)
	if custom != "" {
		if st, err := os.Stat(custom); err == nil && !st.IsDir() {
			return custom, nil
		}
		return "", fmt.Errorf("指定的客户端不存在：%s", custom)
	}

	exeName := clientExeName()
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, exeName),
			filepath.Join(dir, "resources", exeName),
			filepath.Join(dir, "bin", exeName),
			filepath.Join(dir, "..", exeName),
			filepath.Join(dir, "..", "bin", exeName),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, exeName),
			filepath.Join(wd, "bin", exeName),
			filepath.Join(wd, "..", exeName),
			filepath.Join(wd, "..", "bin", exeName),
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
	if p, err := exec.LookPath(exeName); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("rdp2tcp"); err == nil {
		return p, nil
	}
	return "", errors.New("没有找到 " + exeName + "：把客户端放到本程序旁边（或 bin/ 目录），或在「日志与高级」里填写它的完整路径")
}

// loadBinary returns the executable and the flags it understands.
func loadBinary(custom string) *clientBinary {
	path, err := findBinary(custom)
	if err != nil {
		return &clientBinary{err: err}
	}
	binMu.Lock()
	if binCache == nil {
		binCache = map[string]*clientBinary{}
	}
	key := path
	if st, statErr := os.Stat(path); statErr == nil {
		key = fmt.Sprintf("%s|%d", path, st.ModTime().UnixNano())
	}
	if c, ok := binCache[key]; ok {
		binMu.Unlock()
		return c
	}
	binMu.Unlock()

	b := &clientBinary{path: path, flags: probeFlags(path)}
	binMu.Lock()
	binCache[key] = b
	binMu.Unlock()
	return b
}

func parseFlags(raw string) map[string]bool {
	out := map[string]bool{}
	for _, m := range flagLine.FindAllStringSubmatch(raw, -1) {
		out[m[1]] = true
	}
	return out
}

// probeFlags asks the exe for its usage text: Go's flag package prints every
// flag it knows, and those are exactly the flags that will not make it exit(2).
func probeFlags(path string) map[string]bool {
	cmd := exec.Command(path, "-h")
	cmd.SysProcAttr = hiddenProcAttr()
	raw, _ := cmd.CombinedOutput()
	out := parseFlags(string(raw))
	if len(out) == 0 {
		for _, f := range []string{"type", "l", "s", "key", "t", "timeout", "socks", "nolog", "pad", "tls-fp"} {
			out[f] = true
		}
	}
	return out
}

func (b *clientBinary) supports(flag string) bool { return b.flags[flag] }

// args is the command line for one session, built from the flags this exe
// actually has.
func (b *clientBinary) args(p Profile, s Settings) []string {
	var args []string
	add := func(flag string, values ...string) {
		if !b.supports(flag) {
			return
		}
		args = append(args, "-"+flag)
		args = append(args, values...)
	}

	add("type", "client")
	add("mode", p.RouteMode)
	if p.Server != "" {
		add("s", p.Server)
	}
	add("l", p.Listen)
	if p.Key != "" {
		add("key", p.Key)
	}
	add("timeout", fmt.Sprintf("%ds", p.Timeout))

	if p.RouteMode == RouteRule {
		if src := ruleSource(p.Node.RuleIP, "cn-ip.txt"); src != "" {
			add("rule-ip", src)
		}
		if src := ruleSource(p.Node.RuleDomain, "cn-domain.txt"); src != "" {
			add("rule-domain", src)
		}
		add("rule-dns", p.Node.RuleDNS)
	}

	if p.Mode == "socks5" {
		add("socks")
	}
	if p.Mode != "socks5" && p.Target != "" {
		add("t", p.Target)
	}
	for _, spec := range p.Forwards {
		add("L", spec)
	}
	for _, spec := range p.UDPForwards {
		add("U", spec)
	}

	add("tls-fp", p.TLSFP)
	add("pad", p.Pad)
	if p.Pad == "rand" && p.PadMax > 0 {
		add("pad-max", strconv.Itoa(p.PadMax))
	}
	if p.TickMs > 0 {
		add("tick", strconv.Itoa(p.TickMs)+"ms")
	}
	if p.Pin != "" {
		add("pin", p.Pin)
	}
	if p.KnownHosts != "" {
		add("known", p.KnownHosts)
	}
	if p.Insecure {
		add("insecure")
	}
	if s.NoLog {
		add("nolog")
	}
	add("stat", "1")
	return args
}

// commandLine is the same session as one line a terminal can replay.
func commandLine(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArg(bin))
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"&|<>^") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func writeSessionFile(path, line string) error {
	return os.WriteFile(path, []byte(line+"\r\n"), 0o644)
}
