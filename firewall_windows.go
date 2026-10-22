//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// isElevated reports whether this process can talk to netsh directly.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// allowFirewallPort (re)creates one inbound allow rule: delete-then-add, so a
// port change never leaves the old port open behind a stale rule. Elevated
// processes run netsh directly; everyone else gets one UAC prompt that runs
// the very same two commands.
func allowFirewallPort(rule, exe, port string) error {
	if isElevated() {
		removeFirewallRule(rule)
		cmd := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
			"name="+rule,
			"dir=in", "action=allow",
			"program="+exe,
			"protocol=TCP", "localport="+port)
		cmd.SysProcAttr = hiddenProcAttr() // 不闪黑窗
		out, err := cmd.CombinedOutput()
		if err != nil {
			return errors.New("netsh 添加防火墙规则失败：" + strings.TrimSpace(string(out)))
		}
		return nil
	}
	return elevateFirewallAllow(rule, exe, port)
}

// elevateFirewallAllow runs the same delete-then-add through one UAC prompt.
// -EncodedCommand (UTF-16LE base64) sidesteps PowerShell quoting entirely.
func elevateFirewallAllow(rule, exe, port string) error {
	add := fmt.Sprintf(`advfirewall firewall add rule name=%s dir=in action=allow program="%s" protocol=TCP localport=%s`,
		rule, exe, port)
	script := fmt.Sprintf(`try {
  Start-Process -FilePath netsh -ArgumentList 'advfirewall firewall delete rule name=%s' -Verb RunAs -Wait -WindowStyle Hidden
  $p = Start-Process -FilePath netsh -ArgumentList '%s' -Verb RunAs -Wait -PassThru -WindowStyle Hidden
  exit $p.ExitCode
} catch {
  exit 1602
}`, rule, add)

	cmd := exec.Command("powershell", "-NoProfile", "-EncodedCommand",
		base64.StdEncoding.EncodeToString(utf16Bytes(script)))
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1602 {
		return errors.New("防火墙授权弹窗被取消：局域网设备仍无法连入，重新启动可再次弹出")
	}
	return errors.New("以管理员身份放行防火墙失败：" + strings.TrimSpace(string(out)))
}

func utf16Bytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}

// removeFirewallRule deletes the rule. Failures are ignored on purpose: a
// missing rule is the normal case, and a leftover rule only ever admits this
// app's own executable on one port.
func removeFirewallRule(rule string) {
	if !isElevated() {
		return
	}
	cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		"name="+rule)
	cmd.SysProcAttr = hiddenProcAttr()
	_, _ = cmd.CombinedOutput()
}
