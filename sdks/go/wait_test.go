package wave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const taskWaitID = "task_w"

// waitServer is a frame-by-frame controllable fake Wave server for Wait: it
// counts requests per endpoint while per-test closures decide task state,
// required actions, and SSE frames. The state closure receives the server so
// it can branch on other endpoint counters.
type waitServer struct {
	taskGets   atomic.Int64
	actionGets atomic.Int64
	sseGets    atomic.Int64
	taskState  func(s *waitServer, gets int64) string
	actions    []map[string]any
	sse        func(w http.ResponseWriter, r *http.Request, s *waitServer)
}

func newWaitServer(t *testing.T, state func(*waitServer, int64) string, actions []map[string]any, sse func(http.ResponseWriter, *http.Request, *waitServer)) (*Client, *waitServer) {
	t.Helper()
	s := &waitServer{taskState: state, actions: actions, sse: sse}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/events/stream"):
			s.sseGets.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			s.sse(w, r, s)
		case strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			n := s.taskGets.Add(1)
			writeWaitJSON(w, map[string]any{"id": taskWaitID, "session_id": "sess_w", "state": state(s, n)})
		case strings.HasSuffix(r.URL.Path, "/required_actions"):
			s.actionGets.Add(1)
			writeWaitJSON(w, map[string]any{"data": s.actions})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	c, e := New(srv.URL, "wave-test-key")
	if e != nil {
		t.Fatal(e)
	}
	return c, s
}

func writeWaitJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// waitFrame writes one flushed SSE frame whose data carries the awaited
// task_id, so Events' per-task filtering delivers it to Wait.
func waitFrame(w http.ResponseWriter, id, typ string) {
	fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {\"task_id\":%q}\n\n", id, typ, taskWaitID)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestWaitSSETerminal(t *testing.T) {
	c, s := newWaitServer(t,
		func(_ *waitServer, gets int64) string {
			if gets >= 2 {
				return "succeeded"
			}
			return "running"
		},
		nil,
		func(w http.ResponseWriter, r *http.Request, _ *waitServer) {
			waitFrame(w, "1", "task.succeeded")
			<-r.Context().Done() // hold open; Wait cancels once resolved
		})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Hour-long interval keeps the recheck timer inert: only the event may
	// trigger the re-read.
	result, e := c.Wait(ctx, taskWaitID, time.Hour)
	if e != nil || result == nil || result.Reason != "terminal" {
		t.Fatal(result, e)
	}
	if n := s.taskGets.Load(); n != 2 {
		t.Fatalf("task GETs = %d, want 2: initial read + one event-driven re-read", n)
	}
	if n := s.actionGets.Load(); n != 0 {
		t.Fatalf("required_actions GETs = %d, want 0", n)
	}
	if n := s.sseGets.Load(); n != 1 {
		t.Fatalf("SSE connections = %d, want 1", n)
	}
}

func TestWaitSubscribeRace(t *testing.T) {
	c, s := newWaitServer(t,
		func(_ *waitServer, gets int64) string {
			if gets >= 2 {
				return "succeeded"
			}
			return "running"
		},
		nil,
		func(w http.ResponseWriter, r *http.Request, _ *waitServer) {
			// The task finished right after the initial read: the stream
			// stays silent, so only the recheck timer can resolve the wait.
			<-r.Context().Done()
		})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	result, e := c.Wait(ctx, taskWaitID, 40*time.Millisecond)
	elapsed := time.Since(start)
	if e != nil || result == nil || result.Reason != "terminal" {
		t.Fatal(result, e)
	}
	if n := s.taskGets.Load(); n != 2 {
		t.Fatalf("task GETs = %d, want 2", n)
	}
	if n := s.sseGets.Load(); n != 1 {
		t.Fatalf("SSE connections = %d, want 1", n)
	}
	// Resolve within one jittered interval (±10%): no earlier (no busy
	// polling) and far sooner than any timeout.
	if elapsed < 35*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want within one interval", elapsed)
	}
}

func TestWaitReconnectExhaustedFallsBackToPolling(t *testing.T) {
	c, s := newWaitServer(t,
		func(s *waitServer, gets int64) string {
			// Terminal only once every SSE connection attempt is spent.
			if s.sseGets.Load() >= 1+waitMaxReconnects {
				return "succeeded"
			}
			return "running"
		},
		nil,
		func(w http.ResponseWriter, _ *http.Request, _ *waitServer) {
			waitFrame(w, "1", "message.delta") // irrelevant: must not re-probe
			// Drop the connection; Events reconnects until exhausted.
		})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	// Hour-long interval keeps the recheck timer inert: only the polling
	// fallback may resolve after the stream dies.
	result, e := c.Wait(ctx, taskWaitID, time.Hour)
	if e != nil || result == nil || result.Reason != "terminal" {
		t.Fatal(result, e)
	}
	if n := s.taskGets.Load(); n != 2 {
		t.Fatalf("task GETs = %d, want 2: initial read + fallback poll", n)
	}
	if n := s.sseGets.Load(); n != 1+waitMaxReconnects {
		t.Fatalf("SSE connections = %d, want %d", n, 1+waitMaxReconnects)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("elapsed = %v, want the fallback to return promptly", elapsed)
	}
}

func TestWaitContextCancel(t *testing.T) {
	c, _ := newWaitServer(t,
		func(*waitServer, int64) string { return "running" },
		nil,
		func(w http.ResponseWriter, r *http.Request, _ *waitServer) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	start := time.Now()
	result, e := c.Wait(ctx, taskWaitID, time.Hour)
	if e == nil || !errors.Is(e, context.Canceled) {
		t.Fatal(result, e)
	}
	if result != nil {
		t.Fatal(result)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancel took %v to surface", elapsed)
	}
}

func TestWaitApprovalViaToolEvent(t *testing.T) {
	c, s := newWaitServer(t,
		func(_ *waitServer, gets int64) string {
			if gets >= 2 {
				return "waiting"
			}
			return "running"
		},
		[]map[string]any{
			{"task_id": "task_other", "type": "approve_tool", "call_id": "call_other"},
			{"task_id": taskWaitID, "type": "approve_tool", "call_id": "call_1"},
		},
		func(w http.ResponseWriter, r *http.Request, _ *waitServer) {
			waitFrame(w, "1", "tool.result.requested")
			<-r.Context().Done()
		})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, e := c.Wait(ctx, taskWaitID, time.Hour)
	if e != nil || result == nil || result.Reason != "action" || len(result.RequiredActions) != 1 {
		t.Fatal(result, e)
	}
	if result.RequiredActions[0]["call_id"] != "call_1" {
		t.Fatalf("wrong action kept: %v", result.RequiredActions[0])
	}
	if n := s.taskGets.Load(); n != 2 {
		t.Fatalf("task GETs = %d, want 2", n)
	}
	if n := s.actionGets.Load(); n != 1 {
		t.Fatalf("required_actions GETs = %d, want 1", n)
	}
	if n := s.sseGets.Load(); n != 1 {
		t.Fatalf("SSE connections = %d, want 1", n)
	}
}

// Regression: tool.* events must force a required_actions read even while the
// authoritative state is still "running" — approval can be requested before
// the state flips to waiting.
func TestWaitToolEventForcesActionsWhenRunning(t *testing.T) {
	c, s := newWaitServer(t,
		func(*waitServer, int64) string { return "running" },
		[]map[string]any{{"task_id": taskWaitID, "type": "approve_tool", "call_id": "call_1"}},
		func(w http.ResponseWriter, r *http.Request, _ *waitServer) {
			waitFrame(w, "1", "tool.created")
			<-r.Context().Done()
		})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, e := c.Wait(ctx, taskWaitID, time.Hour)
	if e != nil || result == nil || result.Reason != "action" || len(result.RequiredActions) != 1 {
		t.Fatal(result, e)
	}
	if n := s.taskGets.Load(); n != 2 {
		t.Fatalf("task GETs = %d, want 2", n)
	}
	if n := s.actionGets.Load(); n != 1 {
		t.Fatalf("required_actions GETs = %d, want 1: tool.* must force the actions read", n)
	}
}

func TestWaitIgnoresProgressEvents(t *testing.T) {
	c, s := newWaitServer(t,
		func(_ *waitServer, gets int64) string {
			if gets >= 3 {
				return "succeeded"
			}
			return "running"
		},
		nil,
		func(w http.ResponseWriter, r *http.Request, _ *waitServer) {
			waitFrame(w, "1", "message.delta")
			waitFrame(w, "2", "trace.span")
			waitFrame(w, "3", "agent.created")
			waitFrame(w, "4", "task.progress") // relevant: re-read, still running
			waitFrame(w, "5", "message.delta")
			waitFrame(w, "6", "task.succeeded") // relevant: re-read, terminal
			<-r.Context().Done()
		})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, e := c.Wait(ctx, taskWaitID, time.Hour)
	if e != nil || result == nil || result.Reason != "terminal" {
		t.Fatal(result, e)
	}
	if n := s.taskGets.Load(); n != 3 {
		t.Fatalf("task GETs = %d, want 3: progress events must not trigger re-reads", n)
	}
	if n := s.actionGets.Load(); n != 0 {
		t.Fatalf("required_actions GETs = %d, want 0", n)
	}
	if n := s.sseGets.Load(); n != 1 {
		t.Fatalf("SSE connections = %d, want 1", n)
	}
}
