package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The bridge's old code armed one absolute deadline at connection setup, which
// silently killed every tunnel that lived longer than it. These tests shrink
// the head-wait window to 300ms so the regression shows in about two seconds:
// an active tunnel must outlive five windows, a silent one must still be
// reaped when its window passes.

func withShortHeadWait(t *testing.T) {
	t.Helper()
	old := bridgeHeadWait
	bridgeHeadWait = 300 * time.Millisecond
	t.Cleanup(func() { bridgeHeadWait = old })
}

// fakeSocks5 speaks just enough of the protocol for the bridge's dialer:
// no-auth CONNECT, then it pipes to the requested target.
func fakeSocks5(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("socks5 监听失败：%v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSocks5(conn)
		}
	}()
	return ln
}

func serveFakeSocks5(conn net.Conn) {
	defer conn.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if head[0] != 5 {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, head[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	pt := make([]byte, 2)
	if _, err := io.ReadFull(conn, pt); err != nil {
		return
	}
	up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pt)))))
	if err != nil {
		_, _ = conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go func() { _, _ = io.Copy(up, conn) }()
	_, _ = io.Copy(conn, up)
}

// echoTarget answers every byte with itself and stays up: the far end of the
// tunnel under test.
func echoTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo 监听失败：%v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func dialThroughBridge(t *testing.T, bridge *proxyBridge, target string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", bridge.Addr())
	if err != nil {
		t.Fatalf("连桥失败：%v", err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("读取 CONNECT 应答失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT 被拒绝：%s", resp.Status)
	}
	return conn
}

func TestBridgeTunnelSurvivesPastHeadWait(t *testing.T) {
	withShortHeadWait(t)
	target := echoTarget(t)
	socks := fakeSocks5(t)

	bridge, err := startBridge("127.0.0.1:0", socks.Addr().String())
	if err != nil {
		t.Fatalf("启动桥失败：%v", err)
	}
	defer bridge.Close()

	conn := dialThroughBridge(t, bridge, target)

	// An established tunnel has no deadline: it must carry bidirectional data
	// well past five head-wait windows. The old absolute deadline cut it at
	// exactly one.
	rounds := 0
	deadline := time.Now().Add(5*bridgeHeadWait + 500*time.Millisecond)
	for time.Now().Before(deadline) {
		msg := fmt.Sprintf("ping-%d", rounds)
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := conn.Write([]byte(msg)); err != nil {
			t.Fatalf("第 %d 轮写入失败（隧道被提前掐断）：%v", rounds+1, err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatalf("第 %d 轮读取失败（隧道被提前掐断）：%v", rounds+1, err)
		}
		if string(got) != msg {
			t.Fatalf("第 %d 轮数据不符：%q", rounds+1, string(got))
		}
		rounds++
		time.Sleep(bridgeHeadWait / 2)
	}
	if rounds < 5 {
		t.Fatalf("交换轮数不足：只完成了 %d 轮", rounds)
	}
}

func TestBridgeClosesSilentConnection(t *testing.T) {
	withShortHeadWait(t)
	socks := fakeSocks5(t)

	bridge, err := startBridge("127.0.0.1:0", socks.Addr().String())
	if err != nil {
		t.Fatalf("启动桥失败：%v", err)
	}
	defer bridge.Close()

	// A connection that never speaks must be closed once its head wait passes;
	// the browser will simply open another one.
	conn, err := net.Dial("tcp", bridge.Addr())
	if err != nil {
		t.Fatalf("连桥失败：%v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * bridgeHeadWait))
	one := make([]byte, 1)
	_, err = conn.Read(one)
	if err == nil {
		t.Fatal("空闲连接不该收到数据")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("空闲连接超过等待窗口仍未被关闭")
	}
	// EOF or connection reset: the bridge closed it, as intended.
}
