package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSessionReuseAndConfirmedToolFailure(t *testing.T) {
	var initializes, calls, notifications atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		switch body["method"] {
		case "initialize":
			initializes.Add(1)
			w.Header().Set("Mcp-Session-Id", "session-one")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"protocolVersion": protocolVersion}})
		case "notifications/initialized":
			notifications.Add(1)
			if _, ok := body["id"]; ok {
				t.Error("notification has an ID")
			}
			w.WriteHeader(202)
		case "tools/call":
			calls.Add(1)
			if r.Header.Get("Mcp-Session-Id") != "session-one" || r.Header.Get("MCP-Protocol-Version") != protocolVersion {
				t.Error("negotiated session headers missing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\r\n\r\n")
			raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "not found"}}, "isError": true}})
			fmt.Fprintf(w, "data: %s\r\n\r\n", raw)
		default:
			t.Errorf("unneeded MCP request %v", body["method"])
		}
	}))
	defer server.Close()
	client := New("test", server.URL, "")
	client.HTTP = server.Client()
	defer client.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, e := client.Call(context.Background(), "lookup", nil)
			if e != nil || !result.IsError {
				t.Error("lost confirmed failure", result, e)
			}
		})
	}
	wg.Wait()
	if initializes.Load() != 1 || notifications.Load() != 1 || calls.Load() != 8 {
		t.Fatal("session not reused", initializes.Load(), notifications.Load(), calls.Load())
	}
}

func TestMCPReconnectOnlyAfterExpiredSession(t *testing.T) {
	var init, calls, effects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch body["method"] {
		case "initialize":
			n := init.Add(1)
			w.Header().Set("Mcp-Session-Id", fmt.Sprint(n))
			json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "result": map[string]any{"protocolVersion": protocolVersion}})
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/call":
			calls.Add(1)
			if r.Header.Get("Mcp-Session-Id") == "1" {
				w.WriteHeader(404)
				return
			}
			effects.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "result": map[string]any{"content": []any{}, "isError": false}})
		}
	}))
	defer server.Close()
	client := New("test", server.URL, "")
	client.HTTP = server.Client()
	defer client.Close()
	if _, e := client.Call(context.Background(), "write", nil); e != nil {
		t.Fatal(e)
	}
	if init.Load() != 2 || calls.Load() != 2 || effects.Load() != 1 {
		t.Fatal("incorrect reconnect", init.Load(), calls.Load(), effects.Load())
	}
}

func TestMCPDoesNotReplayUncertainCalls(t *testing.T) {
	for _, mode := range []string{"disconnect", "rpc_rejected", "rpc_internal", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["method"] == "initialize" {
					json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "result": map[string]any{"protocolVersion": protocolVersion}})
					return
				}
				if body["method"] == "notifications/initialized" {
					w.WriteHeader(202)
					return
				}
				calls.Add(1)
				switch mode {
				case "disconnect":
					conn, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						conn.Close()
					}
				case "oversized":
					fmt.Fprint(w, strings.Repeat(" ", responseLimit+10))
				default:
					code := -32603
					if mode == "rpc_rejected" {
						code = -32602
					}
					json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "error": map[string]any{"code": code, "message": "server secret must not be reflected"}})
				}
			}))
			defer server.Close()
			client := New("test", server.URL, "")
			client.HTTP = server.Client()
			defer client.Close()
			_, e := client.Call(context.Background(), "write", nil)
			if e == nil || calls.Load() != 1 {
				t.Fatal("uncertain call retried", e, calls.Load())
			}
			var rejected *RejectedError
			if errors.As(e, &rejected) != (mode == "rpc_rejected") {
				t.Fatal("wrong error classification", e)
			}
			if strings.Contains(e.Error(), "secret") {
				t.Fatal("server error body reflected")
			}
		})
	}
}

func TestPoolScopeAndCredentialIsolation(t *testing.T) {
	var pool Pool
	defer pool.Close()
	one := pool.Get("tenant/owner/session", "tool-one", "https://mcp.example", "Bearer a")
	if pool.Get("tenant/owner/session", "tool-two", "https://mcp.example", "Bearer a") != one {
		t.Fatal("same server not reused")
	}
	if pool.Get("tenant/other/session", "tool", "https://mcp.example", "Bearer a") == one {
		t.Fatal("cross-owner session reuse")
	}
	if pool.Get("tenant/owner/session", "tool", "https://mcp.example", "Bearer b") == one {
		t.Fatal("credential rotation reused session")
	}
	for i := range 200 {
		pool.Get(fmt.Sprint(i), "tool", "https://mcp.example", "")
	}
	if len(pool.entries) != 128 {
		t.Fatal("unbounded connection cache", len(pool.entries))
	}
}

func TestDialFailureBeforeDispatchIsConfirmed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected invocation") }))
	client := New("test", server.URL, "")
	client.HTTP = server.Client()
	client.ready = true
	server.Close()
	_, err := client.Call(context.Background(), "write", nil)
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatal("connection refusal became unknown side effect", err)
	}
}
