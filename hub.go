package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// hub fans the live session state out to every open browser tab as
// server-sent events. A tunnel has one truth; every tab watching it sees the
// same status and log lines.

type hubEvent struct {
	Name string
	Data any
}

type subscriber struct {
	ch     chan hubEvent
	closed chan struct{}
	once   sync.Once
}

type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
	seq  int64
}

func newHub() *hub {
	return &hub{subs: map[*subscriber]struct{}{}}
}

func (h *hub) subscribe() *subscriber {
	sub := &subscriber{ch: make(chan hubEvent, 64), closed: make(chan struct{})}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *hub) unsubscribe(sub *subscriber) {
	sub.once.Do(func() { close(sub.closed) })
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
}

func (h *hub) emit(name string, data any) {
	h.mu.Lock()
	// Deliver to slow subscribers by dropping; a live dashboard only needs the
	// newest copy of each event kind, and the next tick sends another.
	for sub := range h.subs {
		select {
		case sub.ch <- hubEvent{Name: name, Data: data}:
		default:
		}
	}
	h.mu.Unlock()
}

// serveSSE answers one browser's event stream. CORS stays off: the API is for
// the page this server embeds.
func (h *hub) serveSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")

	sub := h.subscribe()
	defer h.unsubscribe(sub)

	// An initial comment unblocks the browser's EventSource immediately.
	fmt.Fprint(w, ": open\n\n")
	fl.Flush()

	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev := <-sub.ch:
			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, data)
			fl.Flush()
		}
	}
}
