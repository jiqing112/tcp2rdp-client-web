// tcp2rdp web workbench: the desktop workbench's brain, serving its UI over
// HTTP. Go drives the rdp2tcp client process, SQLite keeps settings, nodes and
// the log tail, and the page (embedded Svelte build) watches over SSE.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// var so -ldflags "-X main.appVersion=…" can stamp release builds.
var appVersion = "1.1.1"

// The frontend build lands in frontend/dist; `npm run build` refreshes it.
//
//go:embed all:frontend/dist
var distFS embed.FS

func main() {
	var (
		addr    = flag.String("addr", "", "HTTP 监听地址（不指定则使用页面里保存的工作台监听设置，首次 127.0.0.1:8080）")
		dataDir = flag.String("data", "", "数据目录（默认：程序目录/data）")
		token   = flag.String("token", "", "访问令牌；设置后页面与 API 都要带上它")
		version = flag.Bool("version", false, "打印版本")
	)
	flag.Parse()
	if *version {
		fmt.Printf("tcp2rdp-web %s %s/%s\n", appVersion, runtime.GOOS, runtime.GOARCH)
		return
	}

	if *dataDir == "" {
		*dataDir = defaultDataDir()
	}
	if abs, err := filepath.Abs(*dataDir); err == nil {
		*dataDir = abs
	}

	store, err := OpenStore(*dataDir)
	if err != nil {
		log.Fatalf("打开数据库失败：%v", err)
	}
	defer store.Close()

	events := newHub()

	// Session log lines reach SQLite through one writer goroutine: the sink
	// runs on the reader goroutines, and a write per line would serialize them.
	var dbWriter sync.WaitGroup
	logCh := make(chan []LogLine, 256)
	dbWriter.Add(1)
	go func() {
		defer dbWriter.Done()
		for batch := range logCh {
			if err := store.AppendLogs(batch); err != nil {
				log.Println("日志入库失败:", err)
			}
		}
	}()
	pending := make([]LogLine, 0, 64)
	var flushMu sync.Mutex
	flush := func() {
		flushMu.Lock()
		defer flushMu.Unlock()
		if len(pending) > 0 {
			batch := make([]LogLine, len(pending))
			copy(batch, pending)
			select {
			case logCh <- batch:
			default: // a full channel means SQLite fell behind; drop the tail
			}
			pending = pending[:0]
		}
	}
	go func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for range tick.C {
			flush()
		}
	}()
	sink := newLogSink(func(lines []LogLine) {
		flushMu.Lock()
		pending = append(pending, lines...)
		ready := len(pending) >= 32
		flushMu.Unlock()
		if ready {
			flush()
		}
	})

	// The previous run's tail is the first thing the page should see.
	if tail, err := store.LoadLogTail(400); err == nil && len(tail) > 0 {
		sink.mu.Lock()
		for _, l := range tail {
			if l.Seq > sink.seq {
				sink.seq = l.Seq
			}
			sink.lines = append(sink.lines, l)
		}
		sink.mu.Unlock()
	}

	tunnel := NewTunnel(store, sink, events, *dataDir)
	api := &api{tunnel: tunnel, store: store, hub: events}
	// A crashed previous run leaves default routes pointing at a dead adapter;
	// repair that before anything else touches the network.
	tunnel.CleanupStaleTun()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/boot", api.handleBoot)
	mux.HandleFunc("/api/info", api.handleInfo)
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			api.handleSettingsGet(w, r)
		case http.MethodPost:
			api.handleSettingsSave(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/status", api.handleStatus)
	mux.HandleFunc("/api/start", api.handleStart)
	mux.HandleFunc("/api/stop", api.handleStop)
	mux.HandleFunc("/api/probe", api.handleProbe)
	mux.HandleFunc("/api/latency", api.handleLatency)
	mux.HandleFunc("/api/sysproxy", api.handleSysProxy)
	mux.HandleFunc("/api/tun", api.handleTun)
	mux.HandleFunc("/api/tun/test", api.handleTunTest)
	mux.HandleFunc("/api/nodes/select", api.handleNodeSelect)
	mux.HandleFunc("/api/nodes/route", api.handleNodeRoute)
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			api.handleLogsGet(w, r)
		case http.MethodDelete:
			api.handleLogsClear(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/locate", api.handleLocate)
	mux.HandleFunc("/api/events", events.serveSSE)
	mux.HandleFunc("/api/weblisten", api.handleWebListen)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSONRaw(w, map[string]string{"ok": "1", "version": appVersion})
	})

	// The embedded SPA: index.html at /, hashed assets with long cache.
	dist, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		log.Fatalf("前端资源不可用：%v", err)
	}
	fileServer := http.FileServer(http.FS(dist))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			r.URL.Path = "/"
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})

	var guard tokenGuard
	if *token == "" {
		guard.token = os.Getenv("TCP2RDP_TOKEN")
	} else {
		guard.token = *token
	}

	srv := &http.Server{
		Handler:           guard.middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 工作台监听地址：显式 -addr 优先；否则用页面里保存的设置（首次 127.0.0.1:8080）。
	// set() 负责绑定、开动服务、放行/撤销防火墙并持久化。
	webHost, webPort, addrExplicit := splitWebListen(*addr, store)
	web := &webListen{srv: srv, store: store, logs: sink, tunnel: tunnel}
	api.web = web
	api.tokenEnabled = guard.enabled()
	if err := web.set(webHost, webPort, !addrExplicit); err != nil {
		log.Fatalf("%v", err)
	}

	fmt.Printf("tcp2rdp-web %s · %s/%s · 数据目录 %s\n", appVersion, platformName(), runtime.GOARCH, shortPath(*dataDir))
	if client, err := findBinary(tunnel.Settings().BinaryPath); err == nil {
		fmt.Printf("客户端：%s\n", shortPath(client))
	} else {
		fmt.Printf("客户端：未找到（%v）\n", err)
	}
	url := fmt.Sprintf("http://%s", web.addr())
	if guard.enabled() {
		url += "/?token=" + guard.token
	}
	fmt.Printf("工作台：%s\n", url)
	if webHost == "0.0.0.0" {
		if lan := lanIPv4(); lan != "" {
			lanURL := fmt.Sprintf("http://%s", net.JoinHostPort(lan, webPort))
			if guard.enabled() {
				lanURL += "/?token=" + guard.token
			}
			fmt.Printf("局域网访问：%s\n", lanURL)
		}
	}

	// Ctrl+C / service stop must not leave the child running, the machine
	// pointed at a dead proxy, or a firewall rule nobody will remove. Shutdown
	// before the HTTP server: the rules matter more than a graceful goodbye.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("正在退出…")
	tunnel.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	flush()
	close(logCh)
	dbWriter.Wait()
}

func writeJSONRaw(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
