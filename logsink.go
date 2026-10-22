package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// LogLine is one line of the session log.
type LogLine struct {
	Seq   int64  `json:"seq"`
	Time  string `json:"time"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// LogBatch is what a page gets each time new lines arrive.
type LogBatch struct {
	Lines []LogLine `json:"lines"`
	Reset bool      `json:"reset"`
}

// logSink keeps a bounded ring of the session log, and hands every accepted
// line to persist so the SQLite store keeps a tail across restarts.
type logSink struct {
	mu          sync.Mutex
	lines       []LogLine
	seq         int64
	minLevel    int
	muted       bool
	limit       int
	quietTarget string
	persist     func([]LogLine)
}

var levelRank = map[string]int{"debug": 0, "app": 1, "info": 1, "warn": 2, "error": 3}

func newLogSink(persist func([]LogLine)) *logSink {
	return &logSink{minLevel: levelRank["info"], limit: 3000, persist: persist}
}

func (s *logSink) setFilter(level string, muted bool) {
	rank, ok := levelRank[strings.ToLower(strings.TrimSpace(level))]
	if !ok {
		rank = levelRank["info"]
	}
	s.mu.Lock()
	s.minLevel = rank
	s.muted = muted
	s.mu.Unlock()
}

func (s *logSink) setQuietTarget(target string) {
	s.mu.Lock()
	s.quietTarget = strings.TrimSpace(target)
	s.mu.Unlock()
}

func (s *logSink) clear() {
	s.mu.Lock()
	s.lines = nil
	s.mu.Unlock()
}

// WriteLine is the entry point for a raw line the client printed.
func (s *logSink) WriteLine(raw string) {
	s.mu.Lock()
	if s.muted || strings.TrimSpace(raw) == "" {
		s.mu.Unlock()
		return
	}
	line := s.appendLocked(raw)
	s.mu.Unlock()
	if line != nil && s.persist != nil {
		s.persist([]LogLine{*line})
	}
}

// returns the stored line so the caller can persist it; nil when filtered.
func (s *logSink) appendLocked(raw string) *LogLine {
	level, when, text := parseLogLine(raw)
	if text == "" {
		return nil
	}
	if level == "info" && isHeartbeat(text) {
		level = "debug"
	}
	if level == "info" && s.quietTarget != "" && strings.HasPrefix(text, s.quietTarget+" 结束") {
		level = "debug"
	}
	if rank, ok := levelRank[level]; ok && rank < s.minLevel {
		return nil
	}
	s.seq++
	line := LogLine{Seq: s.seq, Time: when, Level: level, Text: text}
	s.lines = append(s.lines, line)
	if len(s.lines) > s.limit {
		s.lines = s.lines[len(s.lines)-s.limit:]
	}
	return &line
}

func isHeartbeat(text string) bool {
	return strings.HasPrefix(text, "[stat]")
}

var (
	reStamp  = regexp.MustCompile(`^(\d{2}:\d{2}:\d{2})\s+(.*)$`)
	reSpaces = regexp.MustCompile(`\s{2,}`)
)

// parseLogLine keeps the clock time the client printed and drops the rest.
func parseLogLine(raw string) (level, when, text string) {
	level, when, text = "info", time.Now().Format("15:04:05"), strings.TrimSpace(raw)
	if m := reStamp.FindStringSubmatch(text); m != nil {
		when = m[1]
		text = strings.TrimSpace(m[2])
	}
	text = strings.TrimSpace(reSpaces.ReplaceAllString(text, " "))
	switch {
	case strings.Contains(text, "错误:"), strings.HasPrefix(text, "[错误]"),
		strings.Contains(text, "失败"), strings.Contains(text, "拒绝"):
		level = "error"
	case strings.Contains(text, "[警告]"), strings.Contains(text, "警告"), strings.Contains(text, "重试"):
		level = "warn"
	}
	return level, when, text
}

func (s *logSink) lastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.lines) - 1; i >= 0; i-- {
		if s.lines[i].Level == "error" {
			return s.lines[i].Text
		}
	}
	return ""
}

func (s *logSink) snapshot() []LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogLine, len(s.lines))
	copy(out, s.lines)
	if len(out) > 400 {
		out = out[len(out)-400:]
	}
	return out
}

func (s *logSink) since(seq int64) []LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 0 {
		return nil
	}
	i := len(s.lines)
	for i > 0 && s.lines[i-1].Seq > seq {
		i--
	}
	if i == len(s.lines) {
		return nil
	}
	out := make([]LogLine, len(s.lines)-i)
	copy(out, s.lines[i:])
	return out
}

func (s *logSink) lastSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// debugf adds a line of the app's own.
func (s *logSink) debugf(format string, a ...any) {
	s.mu.Lock()
	line := LogLine{
		Seq:   s.seq + 1,
		Time:  time.Now().Format("15:04:05"),
		Level: "app",
		Text:  fmt.Sprintf(format, a...),
	}
	if !s.muted {
		s.seq = line.Seq
		s.lines = append(s.lines, line)
		if len(s.lines) > s.limit {
			s.lines = s.lines[len(s.lines)-s.limit:]
		}
	}
	s.mu.Unlock()
	if !s.muted && s.persist != nil {
		s.persist([]LogLine{line})
	}
}
