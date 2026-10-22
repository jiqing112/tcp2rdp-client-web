package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// Store keeps everything the web workbench needs to survive a restart in one
// SQLite file: the shared settings, the node list, and the tail of the session
// log. One file, no schema server, works the same on Windows and Linux.

type Store struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败：%w", err)
	}
	path := filepath.Join(dataDir, "tcp2rdp-web.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is happiest with a single writer; the app serializes its
	// own writes behind Store.mu anyway.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS nodes (
  id         TEXT PRIMARY KEY,
  position   INTEGER NOT NULL DEFAULT 0,
  name       TEXT NOT NULL DEFAULT '',
  server     TEXT NOT NULL DEFAULT '',
  key        TEXT NOT NULL DEFAULT '',
  route_mode TEXT NOT NULL DEFAULT 'global',
  rule_basis TEXT NOT NULL DEFAULT 'list',
  rule_ip    TEXT NOT NULL DEFAULT 'builtin',
  rule_domain TEXT NOT NULL DEFAULT 'builtin',
  rule_dns   TEXT NOT NULL DEFAULT '1.1.1.1:53'
);
CREATE TABLE IF NOT EXISTS logs (
  seq  INTEGER PRIMARY KEY AUTOINCREMENT,
  ts   TEXT NOT NULL,
  level TEXT NOT NULL,
  text TEXT NOT NULL
);
`
	_, err := s.db.Exec(ddl)
	return err
}

// ---- settings: a flat key/value table; the JSON shape the API speaks is
// assembled in settings.go on top of these rows. ----

func (s *Store) GetSetting(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// LoadSettingsJSON reads the whole settings object (without nodes) from the kv
// table, which is how it was saved by SaveSettingsJSON.
func (s *Store) LoadSettingsJSON() (map[string]json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}

func (s *Store) SaveSettingsJSON(obj map[string]json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range obj {
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, string(v)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- nodes ----

func (s *Store) LoadNodes() ([]Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, position, name, server, key, route_mode, rule_basis,
		rule_ip, rule_domain, rule_dns FROM nodes ORDER BY position, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var pos int
		if err := rows.Scan(&n.ID, &pos, &n.Name, &n.Server, &n.Key, &n.RouteMode,
			&n.RuleBasis, &n.RuleIP, &n.RuleDomain, &n.RuleDNS); err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SaveNodes replaces the whole list in one transaction, which matches how the
// page edits it: it always sends the complete array.
func (s *Store) SaveNodes(nodes []Node, activeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM nodes`); err != nil {
		return err
	}
	for i, n := range nodes {
		if _, err := tx.Exec(`INSERT INTO nodes(id, position, name, server, key, route_mode,
			rule_basis, rule_ip, rule_domain, rule_dns) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			n.ID, i, n.Name, n.Server, n.Key, n.RouteMode, n.RuleBasis, n.RuleIP, n.RuleDomain, n.RuleDNS); err != nil {
			return err
		}
	}
	if err := setSettingTx(tx, "activeNode", activeID); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- logs ----

// AppendLogs stores a batch of session log lines; called from the log sink's
// writer goroutine only.
func (s *Store) AppendLogs(lines []LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, l := range lines {
		if _, err := tx.Exec(`INSERT INTO logs(ts, level, text) VALUES(?,?,?)`, l.Time, l.Level, l.Text); err != nil {
			return err
		}
	}
	// Keep the table bounded: the page shows a few hundred lines at most.
	if _, err := tx.Exec(`DELETE FROM logs WHERE seq NOT IN (SELECT seq FROM logs ORDER BY seq DESC LIMIT 3000)`); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadLogTail returns the newest count lines with their stored ids as seq.
func (s *Store) LoadLogTail(count int) ([]LogLine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT seq, ts, level, text FROM logs ORDER BY seq DESC LIMIT ?`, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogLine
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.Seq, &l.Time, &l.Level, &l.Text); err != nil {
			continue
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse: the page appends oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) ClearLogs() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM logs`)
	return err
}

func setSettingTx(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// defaultDataDir keeps the database next to the executable when possible so a
// portable folder stays portable, and falls back to the user's home.
func defaultDataDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".tcp2rdp-web")
	}
	return "data"
}

// shortPath keeps a long path readable in the narrow log panel.
func shortPath(p string) string {
	const max = 46
	r := []rune(p)
	if len(r) <= max {
		return p
	}
	return "…" + string(r[len(r)-max+1:])
}
