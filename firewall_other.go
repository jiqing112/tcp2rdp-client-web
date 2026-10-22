//go:build !windows

package main

import "os"

// Windows' BlockInbound default is a Windows idea; elsewhere reaching the port
// is the local packet filter's (ufw/firewalld) business, which this app does
// not presume to reconfigure.
func allowFirewallPort(string, string, string) error { return nil }

func removeFirewallRule(string) {}

// isElevated answers whether this process can change system-wide state. On
// Linux TUN needs CAP_NET_ADMIN, which a root run provides.
func isElevated() bool { return os.Geteuid() == 0 }
