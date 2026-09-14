package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wave-ai.local/wave/internal/modules/execution"
)

func TestLiveAssertionsTraceAndComparison(t *testing.T) {
	t.Setenv("WAVE_API_KEY", "SECRET-key")
	var wrong atomic.Bool
	start := time.Now().Add(-time.Second)
	finished := start.Add(100 * time.Millisecond)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer SECRET-key" {
			t.Error("missing key")
		}
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/v1/agents/agent":
			enc.Encode(map[string]any{"id": "agent", "version": 1, "model": "test"})
		case r.URL.Path == "/v1/sessions":
			enc.Encode(map[string]string{"id": "session"})
		case r.URL.Path == "/v1/sessions/session/tasks":
			enc.Encode(map[string]string{"id": "task"})
		case r.URL.Path == "/v1/tasks/task":
			enc.Encode(execution.Task{ID: "task", RootID: "task", State: "succeeded", CreatedAt: start, StartedAt: &start, FinishedAt: &finished, Result: "SECRET-result", Error: "SECRET-error"})
		case r.URL.Path == "/v1/tasks/task/trace":
			enc.Encode(execution.Trace{Version: 1, TraceID: "task", TaskID: "task", State: "succeeded", Summary: execution.TraceSummary{UsageKnown: true, ModelCalls: 1, ModelMS: 90}})
		case r.URL.Path == "/v1/files":
			enc.Encode(map[string]any{"data": []map[string]string{{"id": "file", "path": "summary.json"}}})
		case r.URL.Path == "/v1/files/file/content":
			if wrong.Load() {
				enc.Encode(map[string]int{"answer": 41})
			} else {
				enc.Encode(map[string]int{"answer": 42})
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	casePath := filepath.Join(dir, "case.json")
	os.WriteFile(casePath, []byte(`{"name":"test","session":{},"task":{"agent_id":"agent","input":"SECRET-prompt"},"expect":{"files":[{"path":"summary.json","json_equals":{"answer":42}}]}}`), 0600)
	var out, diag bytes.Buffer
	args := []string{"-case", casePath, "-url", server.URL, "-tasks", "2", "-clients", "2"}
	if err := runLive(context.Background(), args, &out, &diag); err != nil {
		t.Fatal(err, out.String())
	}
	if strings.Contains(out.String()+diag.String(), "SECRET") {
		t.Fatal("benchmark report leaked content")
	}
	var r report
	if json.Unmarshal(out.Bytes(), &r) != nil || r.Phases[0].Succeeded != 2 || len(r.Phases[0].Tasks) != 2 || r.Phases[0].Tasks[0].Trace == nil {
		t.Fatal(out.String())
	}
	base := filepath.Join(dir, "base.json")
	candidate := filepath.Join(dir, "candidate.json")
	os.WriteFile(base, out.Bytes(), 0600)
	r.RunID = "candidate"
	r.Phases[0].EndToEnd.P95 *= 2
	b, _ := json.Marshal(r)
	os.WriteFile(candidate, b, 0600)
	out.Reset()
	if err := compare([]string{base, candidate}, &out, &diag); err == nil {
		t.Fatal("regression passed")
	}
	if !strings.Contains(out.String(), `"passed": false`) {
		t.Fatal(out.String())
	}
	wrong.Store(true)
	out.Reset()
	if err := runLive(context.Background(), args, &out, &diag); err == nil {
		t.Fatal("incorrect artifact passed")
	}
	if !strings.Contains(out.String(), `"invalid_result": 2`) {
		t.Fatal(out.String())
	}
}

func TestLiveTimeoutRequestsCancellationAndRejectsRedirects(t *testing.T) {
	var canceled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			canceled.Store(true)
			w.WriteHeader(202)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/trace") {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "task", "state": "running"})
	}))
	defer server.Close()
	o := options{Timeout: 20 * time.Millisecond, Poll: time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	s := (apiClient{base: server.URL, http: server.Client()}).liveTask(ctx, liveCase{}, o)
	if s.err == nil || !canceled.Load() || !s.cancelRequested || s.cancelError || !s.traceError || s.state != "timeout" {
		t.Fatalf("%+v", s)
	}
	t.Setenv("WAVE_API_KEY", "secret")
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c, closeClient, err := serviceClient(redirect.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	if c.call(context.Background(), "GET", "/v1/tasks/x", nil, nil) == nil || leaked.Load() {
		t.Fatal("followed credentialed redirect")
	}
}

func TestCaseRequiresAssertionsAndRejectsUnknownFields(t *testing.T) {
	for _, raw := range []string{`{}`, `{"name":"x","task":{"agent_id":"a","input":"x"}}`, `{"name":"x","task":{"agent_id":"a","input":"x"},"expect":{"result_contians":"typo"}}`, `{} {}`} {
		p := filepath.Join(t.TempDir(), "case.json")
		os.WriteFile(p, []byte(raw), 0600)
		if _, e := readCase(p); e == nil {
			t.Fatal("accepted invalid case", raw)
		}
	}
}
