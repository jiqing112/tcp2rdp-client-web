//go:build !windows

package main

import "syscall"

func portOwnerPID(string) int { return 0 }

func processImagePath(int) string { return "" }

// killProcess is used by the stale-TUN cleanup: a SIGKILL leaves no room for
// a dying tun2socks to restore anything.
func killProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}

func hiddenProcAttr() *syscall.SysProcAttr { return nil }
