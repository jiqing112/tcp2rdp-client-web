package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// webListen owns the workbench's own HTTP listener. It can move to another
// scope (127.0.0.1 <-> 0.0.0.0) or another port while the server keeps
// running: established connections — including the SSE streams — keep being
// served after their listener closes, so the tab that made the change never
// loses the conversation mid-answer.
type webListen struct {
	mu     sync.Mutex
	srv    *http.Server
	ln     net.Listener
	host   string
	port   string
	gen    atomic.Int32 // serves run in goroutines; only the newest one reports errors
	store  *Store
	logs   *logSink
	tunnel *Tunnel
}

func (w *webListen) addr() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return net.JoinHostPort(w.host, w.port)
}

// set moves the listener to host:port. Order matters: with a port change the
// new listener binds before the old one closes, so a failed bind never costs
// us the working address; with the same port the old one must be freed first.
func (w *webListen) set(host, port string, persist bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if host == w.host && port == w.port {
		return nil
	}
	target := net.JoinHostPort(host, port)

	if port != w.port {
		newLn, err := net.Listen("tcp", target)
		if err != nil {
			return fmt.Errorf("监听 %s 失败：%w", target, err)
		}
		w.host, w.port = host, port
		w.serve(newLn)
		if w.ln != nil {
			_ = w.ln.Close()
		}
		w.ln = newLn
	} else {
		// Same port, different scope: free it, then take it back.
		if w.ln != nil {
			_ = w.ln.Close()
		}
		newLn, err := net.Listen("tcp", target)
		if err != nil {
			w.host, w.port, w.ln = "", "", nil
			return fmt.Errorf("监听 %s 失败（原监听已关闭，请修正后重试）：%w", target, err)
		}
		w.host, w.port = host, port
		w.serve(newLn)
		w.ln = newLn
	}

	if isWildcardHost(host) {
		if err := w.allowFirewall(port); err != nil {
			w.logs.debugf("防火墙放行未成功（工作台端口 %s）：%v", port, err)
		} else {
			w.logs.debugf("已在 Windows 防火墙放行工作台端口 TCP %s（规则 %s）", port, FirewallRuleUI)
		}
	} else {
		firewallRemove(FirewallRuleUI)
		w.logs.debugf("工作台已改回仅本机访问，已撤销防火墙放行")
	}

	if persist {
		_ = w.store.SetSetting("webHost", host)
		_ = w.store.SetSetting("webPort", port)
	}
	return nil
}

func (w *webListen) allowFirewall(port string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("拿不到程序路径：%w", err)
	}
	return firewallAllow(FirewallRuleUI, exe, port)
}

// serve takes over one listener in the background. Errors from a superseded
// listener (closed on purpose) stay quiet.
func (w *webListen) serve(ln net.Listener) {
	gen := w.gen.Add(1)
	go func() {
		err := w.srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && gen == w.gen.Load() {
			log.Printf("HTTP 服务异常退出：%v", err)
		}
	}()
}

// splitWebListen decides where the workbench listens at startup: an explicit
// -addr wins; otherwise the scope saved from the page (first run: loopback).
func splitWebListen(flagAddr string, store *Store) (host, port string, explicit bool) {
	flagAddr = strings.TrimSpace(flagAddr)
	if flagAddr != "" {
		explicit = true
		switch {
		case isPort(flagAddr):
			return "127.0.0.1", flagAddr, explicit
		case isWildcardHost(flagAddr):
			return "0.0.0.0", "8080", explicit
		}
		if h, p, err := net.SplitHostPort(flagAddr); err == nil {
			return h, p, explicit
		}
		return "127.0.0.1", "8080", explicit
	}
	host, port = "127.0.0.1", "8080"
	if v, ok := store.GetSetting("webHost"); ok {
		if h, err := normalizeWebHost(v); err == nil {
			host = h
		}
	}
	if v, ok := store.GetSetting("webPort"); ok && isPort(v) {
		port = v
	}
	return host, port, false
}
