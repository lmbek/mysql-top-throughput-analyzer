package main

// SSE streaming removed in slimmed build. This file intentionally left minimal.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Toggle to disable the broadcaster/subscribe mechanism and use a simple direct streaming mode.
var sseNoBus bool

// Minimal in-memory ring buffer to keep recent log lines for direct streaming mode.
type logRing struct {
	mu   sync.Mutex
	cap  int
	data []string
	base uint64 // seq number of data[0]
	next uint64 // next seq to assign
}

func newLogRing(capacity int) *logRing { return &logRing{cap: capacity, data: make([]string, 0, capacity)} }

func (r *logRing) Append(s string) {
	r.mu.Lock()
	if len(r.data) == r.cap {
		// drop oldest
		r.data = r.data[1:]
		r.base++
	}
	r.data = append(r.data, s)
	r.next++
	r.mu.Unlock()
}

// GetFrom returns all lines with sequence >= seq.
// It clamps seq to the current base if too old. It returns the new next sequence marker.
func (r *logRing) GetFrom(seq uint64) ([]string, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq < r.base {
		seq = r.base
	}
	if seq > r.next {
		return nil, r.next
	}
	startIdx := int(seq - r.base)
	if startIdx < 0 || startIdx > len(r.data) {
		startIdx = len(r.data)
	}
	out := append([]string(nil), r.data[startIdx:]...)
	return out, r.next
}

// Head returns the current next sequence marker (i.e., position after the last element).
func (r *logRing) Head() uint64 { r.mu.Lock(); defer r.mu.Unlock(); return r.next }

var globalLogRing = newLogRing(2048)

func init() {
	// Allow disabling the bus via env: MON_SSE_NO_BUS = 1|true|yes|on
	v := os.Getenv("MON_SSE_NO_BUS")
	if v != "" {
		lv := strings.ToLower(strings.TrimSpace(v))
		sseNoBus = (lv == "1" || lv == "true" || lv == "yes" || lv == "on")
	}
}

// test hook: allow tests to toggle mode without relying on env parsing order.
func sseSetNoBusForTests(v bool) { sseNoBus = v }

// LogStreamBroadcaster abstracts broadcasting of log lines to subscribers.
type LogStreamBroadcaster interface {
	Subscribe() (chan string, func())
	Broadcast(message string)
}

// sseBroadcaster is the unexported implementation of LogStreamBroadcaster.
type sseBroadcaster struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

// NewLogStreamBroadcaster constructs a LogStreamBroadcaster.
func NewLogStreamBroadcaster() LogStreamBroadcaster {
	return &sseBroadcaster{subs: make(map[chan string]struct{})}
}

// Subscribe registers a new subscriber channel. The caller should consume promptly.
// Returns the channel and an unsubscribe function.
func (b *sseBroadcaster) Subscribe() (chan string, func()) {
	ch := make(chan string, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Broadcast sends a message to all subscribers, dropping if a channel is full.
func (b *sseBroadcaster) Broadcast(message string) {
	b.mu.Lock()
	for ch := range b.subs {
		select {
		case ch <- message:
		default:
			// drop if slow consumer
		}
	}
	b.mu.Unlock()
}

// logTeeWriter is an io.Writer passed to slog JSON handler that also broadcasts
// each newline-terminated JSON log line to the broadcaster.
// It writes through to the underlying writer (e.g., os.Stdout).
type logTeeWriter struct {
	underlying io.Writer
	bus        LogStreamBroadcaster
	mu         sync.Mutex
	buf        bytes.Buffer
}

// NewLogTeeWriter constructs a writer that tees to stdout and to the broadcaster.
func NewLogTeeWriter(w io.Writer, broadcaster LogStreamBroadcaster) io.Writer {
	return &logTeeWriter{underlying: w, bus: broadcaster}
}

func (t *logTeeWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Always forward to underlying writer
	n, err := t.underlying.Write(p)

	// Buffer and emit full lines to the broadcaster
	t.buf.Write(p)
	// Scan for lines
	for {
		data := t.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := string(data[:i])
		// Trim CR if present
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if line != "" {
			// Always append to ring for direct mode or late subscribers
			globalLogRing.Append(line)
			if !sseNoBus && t.bus != nil {
				t.bus.Broadcast(line)
			}
		}
		// advance buffer
		t.buf.Next(i + 1)
	}
	return n, err
}

// LogsSSEHandler streams logs via Server-Sent Events (SSE).
// URL: /logs
func LogsSSEHandler(broadcaster LogStreamBroadcaster, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Headers for SSE and to disable buffering
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// For some proxies/CDNs
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// If broadcaster is disabled, use direct ring-buffer streaming mode.
		if sseNoBus {
			logger.Info("sse client connected (direct mode)", "remote", r.RemoteAddr, "path", r.URL.Path)
			defer logger.Info("sse client disconnected (direct mode)", "remote", r.RemoteAddr, "path", r.URL.Path)

			// Heartbeat interval (default 15s). Allow override for tests via query param "heartbeat".
			heartbeat := 15 * time.Second
			if qs := r.URL.Query().Get("heartbeat"); qs != "" {
				if d, err := time.ParseDuration(qs); err == nil {
					if d < 100*time.Millisecond {
						d = 100 * time.Millisecond
					} else if d > 5*time.Minute {
						d = 5 * time.Minute
					}
					heartbeat = d
				}
			}
			hb := time.NewTicker(heartbeat)
			defer hb.Stop()
			poll := time.NewTicker(200 * time.Millisecond)
			defer poll.Stop()

			// Initial hello event
			fmt.Fprintf(w, "event: hello\n")
			fmt.Fprintf(w, "data: %s\n\n", "{\"msg\":\"connected\"}")
			flusher.Flush()

			notify := r.Context().Done()
			bw := bufio.NewWriter(w)
			seq := globalLogRing.Head()
			for {
				select {
				case <-notify:
					return
				case <-hb.C:
					if _, err := bw.WriteString(": keepalive\n\n"); err != nil { return }
					if err := bw.Flush(); err != nil { return }
					flusher.Flush()
				case <-poll.C:
					lines, next := globalLogRing.GetFrom(seq)
					for _, line := range lines {
						if _, err := bw.WriteString("data: "); err != nil { return }
						if _, err := bw.WriteString(line); err != nil { return }
						if _, err := bw.WriteString("\n\n"); err != nil { return }
						if err := bw.Flush(); err != nil { return }
						flusher.Flush()
					}
					seq = next
				}
			}
		}

		logger.Info("sse client connected", "remote", r.RemoteAddr, "path", r.URL.Path)
		defer logger.Info("sse client disconnected", "remote", r.RemoteAddr, "path", r.URL.Path)

		ch, unsubscribe := broadcaster.Subscribe()
		defer unsubscribe()

		// Heartbeat interval (default 15s). Allow override for tests via query param "heartbeat".
		heartbeat := 15 * time.Second
		if qs := r.URL.Query().Get("heartbeat"); qs != "" {
			if d, err := time.ParseDuration(qs); err == nil {
				// clamp to sane bounds
				if d < 100*time.Millisecond {
					d = 100 * time.Millisecond
				} else if d > 5*time.Minute {
					d = 5 * time.Minute
				}
				heartbeat = d
			}
		}
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()

		// Send a hello event so clients know the stream is up
		fmt.Fprintf(w, "event: hello\n")
		fmt.Fprintf(w, "data: %s\n\n", "{\"msg\":\"connected\"}")
		flusher.Flush()

		// Use a buffered writer pattern to loop until client disconnects
		notify := r.Context().Done()
		bw := bufio.NewWriter(w)
		for {
			select {
			case <-notify:
				return
			case <-ticker.C:
				// SSE comment line as heartbeat; many clients/proxies treat this as keep-alive
				if _, err := bw.WriteString(": keepalive\n\n"); err != nil {
					return
				}
				if err := bw.Flush(); err != nil {
					return
				}
				flusher.Flush()
			case line, ok := <-ch:
				if !ok {
					return
				}
				// SSE format: data: <json>\n\n
				if _, err := bw.WriteString("data: "); err != nil {
					return
				}
				if _, err := bw.WriteString(line); err != nil {
					return
				}
				if _, err := bw.WriteString("\n\n"); err != nil {
					return
				}
				if err := bw.Flush(); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
