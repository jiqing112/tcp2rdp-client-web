package main

import (
	"fmt"
	"strings"
)

// Firewall rule names mark the two rules this app manages. They are recreated
// on every LAN-shared start so their ports and programs always match what is
// actually running:
//
//	tcp2rdp-web        — the workbench page itself (-addr 0.0.0.0:…)
//	tcp2rdp-web-tunnel — the tunnel's listen port (SOCKS5 / forwards, -l 0.0.0.0:…)
const (
	FirewallRuleUI     = "tcp2rdp-web"
	FirewallRuleTunnel = "tcp2rdp-web-tunnel"
)

// The service hooks call through these indirections so tests can swap in
// stubs: a test run that binds 0.0.0.0 must not edit the machine's firewall.
var (
	firewallAllow  = allowFirewallPort
	firewallRemove = removeFirewallRule
)

// isWildcardHost reports whether an address means "every interface", which is
// what makes a port reachable from the LAN (and thus a firewall matter).
func isWildcardHost(host string) bool {
	switch host {
	case "", "0.0.0.0", "*", "::", "[::]":
		return true
	}
	return false
}

// normalizeWebHost accepts the two answers a listen scope can have: this
// machine, or every interface. Anything else is a typo.
func normalizeWebHost(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "127.0.0.1", "localhost", "本机":
		return "127.0.0.1", nil
	case "0.0.0.0", "*", "::", "lan":
		return "0.0.0.0", nil
	}
	return "", fmt.Errorf("监听范围只支持 127.0.0.1 或 0.0.0.0：%q", strings.TrimSpace(v))
}
