//go:build darwin

package main

import "errors"

// macOS builds ship without TUN: routing the whole system through a utun
// device needs utun control + scoped resolvers this workbench does not wire
// up yet. Everything else (SOCKS5/HTTP, -L/-U forwards, system probe) is
// unaffected; the UI reports the mode as unavailable.

func tunPlatformSupported() bool { return false }

func tunPlatformCheck() error {
	return errors.New("TUN 模式暂不支持 macOS，请使用 SOCKS5 / 端口转发模式")
}

func startTunPlatform(*Tunnel, Profile, Settings) (*tunSession, error) {
	return nil, tunPlatformCheck()
}

func stopTunPlatform(*tunSession) error { return nil }

func testTunPlatform(*tunSession, string) TunReport { return TunReport{} }

func cleanupStaleTunPlatform(*Store, func(string, ...any)) {}
