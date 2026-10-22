//go:build !windows

package main

// Windows' BlockInbound default is a Windows idea; elsewhere reaching the port
// is the local packet filter's (ufw/firewalld) business, which this app does
// not presume to reconfigure.
func allowFirewallPort(string, string, string) error { return nil }

func removeFirewallRule(string) {}
