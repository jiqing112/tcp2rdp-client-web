package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// measureTCP is the direct-connection mode: one handshake to the target, no
// tunnel involved.
func measureTCP(target string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return 0, fmt.Errorf("TCP 连接 %s 失败: %w", target, err)
	}
	elapsed := time.Since(start)
	_ = conn.Close()
	return elapsed, nil
}

// The reply line carries "TTL=" / "ttl=" in every locale, while the time label
// is localized ("time=" / "时间="). Matching the number right before "ms"
// instead of the label keeps this working on a Chinese system without decoding
// the console's GBK output.
var rePingTime = regexp.MustCompile(`[=<]\s*([0-9]+(?:\.[0-9]+)?)\s*ms`)

// measurePing runs the system ping once, which is the only way to send real
// ICMP without raw-socket privileges. Flags differ per OS; the output parsing
// relies on ASCII only.
func measurePing(host string, timeout time.Duration) (time.Duration, error) {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return 0, errors.New("没有探测目标")
	}
	waitMs := int(timeout / time.Millisecond)
	if waitMs < 500 {
		waitMs = 500
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+3*time.Second)
	defer cancel()

	var args []string
	switch runtime.GOOS {
	case "windows":
		args = []string{"-n", "1", "-w", strconv.Itoa(waitMs), host}
	case "darwin":
		args = []string{"-c", "1", "-t", strconv.Itoa(int(timeout/time.Second) + 1), host}
	default: // linux and the rest of the unix family
		args = []string{"-c", "1", "-W", strconv.Itoa(int(timeout/time.Second) + 1), host}
	}
	cmd := exec.CommandContext(ctx, "ping", args...)
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.CombinedOutput()
	if d, ok := parsePingDuration(string(out)); ok {
		return d, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return 0, fmt.Errorf("ping %s 超时", host)
	}
	if err != nil {
		return 0, fmt.Errorf("ping %s 没有回包（目标可能禁 ping，或无法解析）", host)
	}
	return 0, fmt.Errorf("ping %s 没有可用的延迟数据", host)
}

func parsePingDuration(text string) (time.Duration, bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(strings.ToLower(line), "ttl=") {
			continue
		}
		m := rePingTime.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		d := time.Duration(math.Round(value * float64(time.Millisecond)))
		if d < time.Millisecond {
			d = time.Millisecond
		}
		return d, true
	}
	return 0, false
}

// dataPathTarget is the host:port used to prove the tunnel carries bytes.
func dataPathTarget(s Settings) string {
	target := strings.TrimSpace(s.ProbeTarget)
	if host, port, ok := splitHostPort(target); ok {
		return net.JoinHostPort(host, port)
	}
	host := strings.Trim(target, "[]")
	if host != "" && !strings.ContainsAny(host, " :/") {
		return net.JoinHostPort(host, "443")
	}
	if ip := net.ParseIP(host); ip != nil {
		return net.JoinHostPort(host, "443")
	}
	return "www.baidu.com:443"
}
