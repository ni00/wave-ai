package modelclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sse serves one streamed chat-completions response from the given frames.
func sse(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			w.Write([]byte("data: " + f + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
}

// Regression: providers may emit tool_calls with sparse, non-contiguous
// indices (e.g. 0 and 2). Arguments must land on the right call, not be
// dropped or shifted.
func TestStreamSparseToolCallIndices(t *testing.T) {
	srv := sse(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_c","type":"function","function":{"name":"read","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":2,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":2,"function":{"arguments":"\"/x\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	})
	defer srv.Close()

	c := New(srv.URL, "k", 0)
	final, err := c.Stream(t.Context(), Request{Model: "m", Stream: true}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(final.ToolCalls) != 2 {
		t.Fatalf("want 2 tool calls, got %d: %+v", len(final.ToolCalls), final.ToolCalls)
	}
	a, b := final.ToolCalls[0], final.ToolCalls[1]
	if a.ID != "call_a" || a.Function.Name != "bash" || a.Function.Arguments != `{"command":"ls"}` {
		t.Errorf("call 0 wrong: %+v", a)
	}
	if b.ID != "call_c" || b.Function.Name != "read" || b.Function.Arguments != `{"path":"/x"}` {
		t.Errorf("call 2 wrong: %+v", b)
	}
	if final.Usage.PromptTokens == nil || *final.Usage.PromptTokens != 10 {
		t.Errorf("usage not captured: %+v", final.Usage)
	}
	if final.StopReason != "tool_calls" {
		t.Errorf("stop reason = %q", final.StopReason)
	}
}

func TestStreamContentAndUsage(t *testing.T) {
	srv := sse(t, []string{
		`{"choices":[{"delta":{"content":"he"}}]}`,
		`{"choices":[{"delta":{"content":"llo"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	})
	defer srv.Close()

	var got strings.Builder
	c := New(srv.URL, "k", 0)
	final, err := c.Stream(t.Context(), Request{Model: "m", Stream: true}, func(d Delta) {
		got.WriteString(d.Text)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.String() != "hello" {
		t.Errorf("deltas = %q", got.String())
	}
	if len(final.Content) != 1 || final.Content[0].Text != "hello" {
		t.Errorf("final content = %+v", final.Content)
	}
}

func TestTruncatedStreamIsNotSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	}))
	defer server.Close()
	if _, e := New(server.URL, "", 0).Stream(t.Context(), Request{Model: "test"}, nil); e == nil {
		t.Fatal("truncated preview became success")
	}
}

func TestOutputBudgetReachesProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		if body["max_completion_tokens"] != float64(512) {
			t.Error("missing provider output cap", body["max_completion_tokens"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"summary\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	if _, e := New(server.URL, "", 0).Stream(t.Context(), Request{Model: "any", MaxOutputTokens: 512}, nil); e != nil {
		t.Fatal(e)
	}
}
