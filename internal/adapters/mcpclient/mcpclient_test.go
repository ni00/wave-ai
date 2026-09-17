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
	"time"
)

func TestSessionReuseAndConfirmedToolFailure(t *testing.T) {
	var initializes, calls, notifications atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
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
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch body["method"] {
		case "initialize":
			n := init.Add(1)
			w.Header().Set("Mcp-Session-Id", fmt.Sprint(n))
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"protocolVersion": protocolVersion}})
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/call":
			calls.Add(1)
			if r.Header.Get("Mcp-Session-Id") == "1" {
				w.WriteHeader(404)
				return
			}
			effects.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"content": []any{}, "isError": false}})
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
				if r.Method == http.MethodDelete {
					w.WriteHeader(204)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["method"] == "initialize" {
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"protocolVersion": protocolVersion}})
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
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "error": map[string]any{"code": code, "message": "server secret must not be reflected"}})
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

func TestEvictedClientRefusesCalls(t *testing.T) {
	client := New("tool", "https://mcp.example", "")
	client.Close()
	if _, err := client.Call(t.Context(), "ping", nil); err == nil {
		t.Fatal("evicted client accepted a call")
	} else {
		var rejected *RejectedError
		if !errors.As(err, &rejected) {
			t.Fatalf("not a rejection: %v", err)
		}
	}
}

func TestDialFailureBeforeDispatchIsConfirmed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["method"] == "initialize" {
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"protocolVersion": protocolVersion}})
		} else {
			w.WriteHeader(202)
		}
	}))
	client := New("test", server.URL, "")
	client.HTTP = server.Client()
	defer client.Close()
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	_, err := client.Call(context.Background(), "write", nil)
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatal("connection refusal became unknown side effect", err)
	}
}

// These tests use real HTTP exchange boundaries: transport errors alone do not
// establish whether the remote tool already produced a side effect.
func TestMCPPolicyBoundaries(t *testing.T) {
	for _, mode := range []string{"timeout", "401", "403", "429", "500", "redirect", "sse_limit", "input_required", "expired_twice"} {
		t.Run(mode, func(t *testing.T) {
			var calls, gets, inits, redirects atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1); w.WriteHeader(200) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					w.WriteHeader(204)
					return
				}
				if r.Method == "GET" {
					gets.Add(1)
					w.WriteHeader(405)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("missing scoped credential")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				respond := func(result any) {
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": result})
				}
				switch body["method"] {
				case "initialize":
					inits.Add(1)
					w.Header().Set("Mcp-Session-Id", "session")
					respond(map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "test", "version": "1"}})
					return
				case "notifications/initialized", "notifications/cancelled":
					w.WriteHeader(202)
					return
				case "tools/call":
					calls.Add(1)
				default:
					t.Errorf("unexpected method %v", body["method"])
					return
				}
				switch mode {
				case "timeout":
					time.Sleep(100 * time.Millisecond)
					respond(map[string]any{"content": []any{}})
				case "401":
					http.Error(w, "test-secret", 401)
				case "403":
					http.Error(w, "test-secret", 403)
				case "429":
					http.Error(w, "test-secret", 429)
				case "500":
					http.Error(w, "test-secret", 500)
				case "redirect":
					w.Header().Set("Location", target.URL)
					w.WriteHeader(307)
				case "expired_twice":
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "error": map[string]any{"code": -32001, "message": "expired"}})
				case "sse_limit":
					w.Header().Set("Content-Type", "text/event-stream")
					// Individually small comment events must still hit the cumulative bound.
					for range responseLimit/1024 + 2 {
						fmt.Fprintf(w, ":%s\n\n", strings.Repeat("x", 1024))
					}
					raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": map[string]any{"content": []any{}}})
					fmt.Fprintf(w, "data: %s\n\n", raw)
				case "input_required":
					respond(map[string]any{"resultType": "input_required", "requestState": "state", "inputRequests": map[string]any{}})
				}
			}))
			defer server.Close()
			client := New("test", server.URL, "Bearer test-secret")
			client.HTTP = server.Client()
			defer client.Close()
			initCtx, cancelInit := context.WithCancel(context.Background())
			if err := client.Initialize(initCtx); err != nil {
				t.Fatal(err)
			}
			cancelInit() // Initialization's context must not own the reusable session.
			ctx := context.Background()
			if mode == "timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
				defer cancel()
			}
			_, err := client.Call(ctx, "write", nil)
			var rejected *RejectedError
			wantRejected := mode == "401" || mode == "403" || mode == "expired_twice"
			wantCalls := int32(1)
			if mode == "expired_twice" {
				wantCalls = 2
			}
			if err == nil || errors.As(err, &rejected) != wantRejected || calls.Load() != wantCalls || gets.Load() != 0 || redirects.Load() != 0 {
				t.Fatalf("err=%v calls=%d gets=%d redirects=%d", err, calls.Load(), gets.Load(), redirects.Load())
			}
			if strings.Contains(err.Error(), "test-secret") {
				t.Fatal("credential reflected")
			}
			if inits.Load() != wantCalls {
				t.Fatal("unexpected reconnect", inits.Load())
			}
		})
	}
}
