package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// api is the REST surface the page talks to. Everything that changes state
// returns the fresh status (and settings where they changed), so a browser that
// prefers one-shot calls over the SSE stream still stays in sync.

type api struct {
	tunnel *Tunnel
	store  *Store
	hub    *hub
	web    *webListen
	mu     sync.Mutex // serializes mutating calls: one session at a time

	tokenEnabled bool // set by main once the token guard is configured
}

func (a *api) info() Info {
	inf := a.tunnel.Info(a.web.host, a.web.port)
	inf.TokenRequired = a.tokenEnabled
	return inf
}

type bootPayload struct {
	Info     Info      `json:"info"`
	Settings Settings  `json:"settings"`
	Status   Status    `json:"status"`
	Logs     []LogLine `json:"logs"`
}

func (a *api) handleBoot(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, bootPayload{
		Info:     a.info(),
		Settings: a.tunnel.Settings(),
		Status:   a.tunnel.Status(),
		Logs:     a.tunnel.Logs(),
	})
}

func (a *api) handleInfo(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, a.info())
}

// handleWebListen moves the workbench's own listener to another scope or port
// while the server keeps running. The fresh Info in the answer carries the new
// addresses; a changed port means the current tab's address is stale.
func (a *api) handleWebListen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Port string `json:"port"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	host, err := normalizeWebHost(req.Host)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	port := strings.TrimSpace(req.Port)
	if !isPort(port) {
		a.writeErr(w, fmt.Errorf("端口无效：%q，应为 1-65535", port))
		return
	}
	// The workbench must not fight the tunnel for a port: rdp2tcp binds these
	// on start, and whoever comes second fails with a bare bind error.
	s := a.tunnel.Settings()
	if port == s.ListenPort {
		a.writeErr(w, fmt.Errorf("端口 %s 与隧道本地监听端口相同，换一个", port))
		return
	}
	if port == s.HTTPPort {
		a.writeErr(w, fmt.Errorf("端口 %s 与本地 HTTP 代理端口相同，换一个", port))
		return
	}
	if err := a.web.set(host, port, true); err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, a.info())
}

func (a *api) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, a.tunnel.Settings())
}

func (a *api) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	var s Settings
	if !a.readBody(w, r, &s) {
		return
	}
	saved, err := a.tunnel.SaveSettings(s)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, saved)
}

func (a *api) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, a.tunnel.Status())
}

func (a *api) handleStart(w http.ResponseWriter, r *http.Request) {
	var s Settings
	if !a.readBody(w, r, &s) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, err := a.tunnel.Start(s)
	if err != nil {
		// The state still moved (usually to error): report both.
		a.writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error":  err.Error(),
			"status": st,
		})
		return
	}
	a.writeJSON(w, st)
}

func (a *api) handleStop(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, _ := a.tunnel.Stop()
	a.writeJSON(w, st)
}

func (a *api) handleNodeSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, err := a.tunnel.SelectNode(req.ID)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"status": st, "settings": a.tunnel.Settings()})
}

func (a *api) handleNodeRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		RouteMode string `json:"routeMode"`
		RuleBasis string `json:"ruleBasis"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, err := a.tunnel.SetNodeRoute(req.ID, req.RouteMode, req.RuleBasis)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"status": st, "settings": a.tunnel.Settings()})
}

func (a *api) handleProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Server string `json:"server"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	res, err := ProbeServer(req.Server)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, res)
}

func (a *api) handleLatency(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode     string `json:"mode"`
		Target   string `json:"target"`
		Interval int    `json:"interval"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	saved, err := a.tunnel.SetLatency(req.Mode, req.Target, req.Interval)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, map[string]any{"settings": saved, "status": a.tunnel.Status()})
}

func (a *api) handleSysProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	st, err := a.tunnel.SetSystemProxy(req.On)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, st)
}

// handleTun flips the TUN preset; handleTunTest runs the multi-point self-test
// against the live session.
func (a *api) handleTun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if !a.readBody(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, err := a.tunnel.SetTun(req.On)
	if err != nil {
		a.writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error":  err.Error(),
			"status": st,
		})
		return
	}
	a.writeJSON(w, st)
}

func (a *api) handleTunTest(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	report, err := a.tunnel.TestTun()
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, report)
}

func (a *api) handleLogsGet(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, a.tunnel.Logs())
}

func (a *api) handleLogsClear(w http.ResponseWriter, r *http.Request) {
	a.tunnel.ClearLogs()
	a.hub.emit("logs", LogBatch{Lines: []LogLine{}, Reset: true})
	a.writeJSON(w, map[string]bool{"ok": true})
}

func (a *api) handleLocate(w http.ResponseWriter, r *http.Request) {
	server := strings.TrimSpace(r.URL.Query().Get("server"))
	info, err := a.tunnel.Locate(server)
	if err != nil {
		a.writeErr(w, err)
		return
	}
	a.writeJSON(w, info)
}

// ---- plumbing ----

func (a *api) readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		a.writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效：" + err.Error()})
		return false
	}
	return true
}

func (a *api) writeJSON(w http.ResponseWriter, v any) {
	a.writeJSONStatus(w, http.StatusOK, v)
}

func (a *api) writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *api) writeErr(w http.ResponseWriter, err error) {
	a.writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// ---- auth ----

// tokenGuard enforces an optional shared secret: with -token set, every API
// call and event stream must carry it. The frontend itself is served openly so
// the page can show a "need token" message instead of a bare 401.
type tokenGuard struct {
	token string
}

func (g *tokenGuard) enabled() bool { return g.token != "" }

func (g *tokenGuard) check(r *http.Request) bool {
	if !g.enabled() {
		return true
	}
	auth := r.Header.Get("Authorization")
	if strings.TrimPrefix(auth, "Bearer ") == g.token {
		return true
	}
	if r.URL.Query().Get("token") == g.token {
		return true
	}
	return false
}

func (g *tokenGuard) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !g.check(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "需要访问令牌：在地址后加 ?token=… 或请求头 Authorization: Bearer …"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
