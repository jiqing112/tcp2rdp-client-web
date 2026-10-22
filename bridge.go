package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// proxyBridge is an HTTP proxy in front of the tunnel's socks5 port. Windows'
// system proxy setting cannot speak SOCKS, so bringing the tunnel to "global"
// on Windows means handing WinINET apps an HTTP proxy that forwards into socks5.
type proxyBridge struct {
	ln   net.Listener
	dial proxy.Dialer
	stop chan struct{}
	addr string
}

func startBridge(listenAddr, socksAddr string) (*proxyBridge, error) {
	d, err := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: 20 * time.Second})
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	b := &proxyBridge{ln: ln, dial: d, stop: make(chan struct{}), addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(conn)
		}
	}()
	return b, nil
}

func (b *proxyBridge) Addr() string { return b.addr }

func (b *proxyBridge) Close() {
	select {
	case <-b.stop:
	default:
		close(b.stop)
	}
	_ = b.ln.Close()
}

// bridgeHeadWait bounds how long the bridge waits for the next request head on
// a client connection: browsers open connection pools and either speak at once
// or abandon them, so a silent one is closed instead of piling up. It must
// never govern an established tunnel — an HTTPS session can sit quiet for
// minutes (idle keep-alive, long polling) and has to live exactly as long as
// its two ends do.
var bridgeHeadWait = 2 * time.Minute

func (b *proxyBridge) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(bridgeHeadWait))
		req, err := http.ReadRequest(br)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			return
		}
		if req.Method == http.MethodConnect {
			b.tunnel(conn, br, req.Host, "443")
			return
		}
		if !b.forward(conn, req) {
			return
		}
		if req.Close {
			return
		}
	}
}

// tunnel answers a CONNECT and then copies bytes both ways through socks5.
func (b *proxyBridge) tunnel(conn net.Conn, br *bufio.Reader, host, defaultPort string) {
	// The head wait armed above is done once the request is read; the tunnel
	// itself runs without deadlines, so a connection that is merely idle is not
	// mistaken for a dead one.
	_ = conn.SetDeadline(time.Time{})
	target := withPort(host, defaultPort)
	upstream, err := b.dial.Dial("tcp", target)
	if err != nil {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\nProxy-Agent: tcp2rdp-workbench\r\n\r\n")
		return
	}
	defer upstream.Close()
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 200 Connection Established\r\nProxy-Agent: tcp2rdp-workbench\r\n\r\n"); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(upstream, br)
		// A finished direction must not leave the other one blocked on a
		// session whose both ends are gone; closing upstream unblocks the
		// copy below, and the defers close the rest.
		_ = upstream.Close()
	}()
	_, _ = io.Copy(conn, upstream)
}

// forward sends one plain HTTP request through the tunnel and streams the
// answer back: GET http://host/path arrives with an absolute URI.
func (b *proxyBridge) forward(conn net.Conn, req *http.Request) bool {
	if req.URL == nil || req.URL.Host == "" {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return false
	}
	target := withPort(req.URL.Host, "80")
	upstream, err := b.dial.Dial("tcp", target)
	if err != nil {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return false
	}
	defer upstream.Close()
	req.RequestURI = ""
	req.Header.Del("Proxy-Connection")
	if err := req.Write(upstream); err != nil {
		return false
	}
	_, _ = io.Copy(conn, upstream)
	return false // one request per connection keeps this simple and correct
}

func withPort(host, port string) string {
	if strings.Contains(host, ":") {
		return host
	}
	return net.JoinHostPort(host, port)
}

func socksDialer(addr string, timeout time.Duration) (proxy.Dialer, error) {
	return proxy.SOCKS5("tcp", addr, nil, &net.Dialer{Timeout: timeout})
}

// probeDataPath proves the tunnel moves real bytes, not just handshakes: a
// socks5 CONNECT that completes is the only signal worth trusting before
// handing the machine's traffic to it. It also returns how long the round trip
// took, which is the latency the dashboard plots.
func probeDataPath(socksAddr, target string, timeout time.Duration) (time.Duration, error) {
	if socksAddr == "" {
		return 0, fmt.Errorf("没有 socks5 端口")
	}
	d, err := socksDialer(socksAddr, timeout)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	conn, err := d.Dial("tcp", target)
	if err != nil {
		return 0, fmt.Errorf("通过隧道连接 %s 失败: %w", target, err)
	}
	elapsed := time.Since(start)
	_ = conn.Close()
	return elapsed, nil
}
