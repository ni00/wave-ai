package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/config"
)

func TestHTTPCluster(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := Init(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model    string `json:"model"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			t.Error(e)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		done := false
		for _, m := range in.Messages {
			if m.Role == "tool" {
				done = true
			}
		}
		if in.Model == "writer" && !done {
			frame := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "write-output", "type": "function", "function": map[string]any{"name": "write", "arguments": `{"path":"/mnt/session/outputs/result.txt","content":"artifact"}`}}}}}}}
			b, _ := json.Marshal(frame)
			fmt.Fprintf(w, "data: %s\n\n", b)
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"completed\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	dir := t.TempDir()
	cfg := &config.Config{Role: "worker", DatabaseURL: dsn, DataDir: dir, SandboxBackend: "local", SandboxLocalRoot: dir + "/sandboxes", WorkerConcurrency: 4, LeaseSeconds: 5, ModelBaseURL: model.URL, ModelTimeoutSec: 10}
	a, e := New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	api1 := httptest.NewServer(a.Handler())
	defer api1.Close()
	api2 := httptest.NewServer(b.Handler())
	defer api2.Close()
	key, e := auth.Bootstrap(ctx, a.DB, "cluster-"+fmt.Sprint(time.Now().UnixNano()), "owner", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	call := func(base, method, path string, in any, code int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != code {
			t.Fatalf("%s %s = %d: %s", method, path, res.StatusCode, data)
		}
		out := map[string]any{}
		if len(data) > 0 {
			if e = json.Unmarshal(data, &out); e != nil {
				t.Fatal(e)
			}
		}
		return out
	}
	agent := call(api1.URL, "POST", "/v1/agents", map[string]any{"name": "model only", "model": "arbitrary-model"}, 201)
	session := call(api1.URL, "POST", "/v1/sessions", map[string]any{"title": "without a sandbox"}, 201)
	sid := session["id"].(string)
	task := call(api1.URL, "POST", "/v1/sessions/"+sid+"/tasks", map[string]any{"agent_id": agent["id"], "input": "test"}, 202)
	tid := task["id"].(string)
	stream := func(base string, after string) <-chan int64 {
		ch := make(chan int64, 1)
		go func() {
			req, _ := http.NewRequestWithContext(ctx, "GET", base+"/v1/sessions/"+sid+"/events/stream", nil)
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Last-Event-ID", after)
			res, e := http.DefaultClient.Do(req)
			if e != nil {
				return
			}
			defer res.Body.Close()
			sc := bufio.NewScanner(res.Body)
			var seq int64
			for sc.Scan() {
				line := sc.Text()
				if strings.HasPrefix(line, "id: ") {
					fmt.Sscan(strings.TrimPrefix(line, "id: "), &seq)
				}
				if line == "event: task.finished" {
					ch <- seq
					return
				}
			}
		}()
		return ch
	}
	first, second := stream(api1.URL, "0"), stream(api2.URL, "0")
	workerDone := make(chan error, 1)
	go func() { workerDone <- a.Run(ctx) }()
	defer func() { cancel(); <-workerDone }()
	var finalSeq int64
	for _, ch := range []<-chan int64{first, second} {
		select {
		case seq := <-ch:
			if seq == 0 {
				t.Fatal("missing SSE id")
			}
			if finalSeq != 0 && seq != finalSeq {
				t.Fatal("API replicas disagree")
			}
			finalSeq = seq
		case <-time.After(10 * time.Second):
			t.Fatal("SSE completion timed out")
		}
	}
	out := call(api2.URL, "GET", "/v1/tasks/"+tid, nil, 200)
	if out["state"] != "succeeded" {
		t.Fatal(out)
	}
	messages := call(api2.URL, "GET", "/v1/sessions/"+sid+"/messages", nil, 200)["data"].([]any)
	if len(messages) != 2 || messages[1].(map[string]any)["text"] != "completed" {
		t.Fatal("durable HTTP history", messages)
	}
	generations := call(api2.URL, "GET", "/v1/tasks/"+tid+"/generations", nil, 200)["data"].([]any)
	if len(generations) != 1 || generations[0].(map[string]any)["message_id"] != messages[1].(map[string]any)["id"] {
		t.Fatal("generation attribution", generations)
	}
	actions := call(api2.URL, "GET", "/v1/sessions/"+sid+"/required_actions", nil, 200)["data"].([]any)
	if len(actions) != 0 {
		t.Fatal("stale actions", actions)
	}
	history := call(api2.URL, "GET", "/v1/sessions/"+sid+"/events?after="+fmt.Sprint(finalSeq-1), nil, 200)
	if len(history["data"].([]any)) != 1 {
		t.Fatal("event replay skipped or duplicated")
	}
	replay := stream(api2.URL, fmt.Sprint(finalSeq-1))
	select {
	case n := <-replay:
		if n != finalSeq {
			t.Fatal("wrong resumed event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE resume failed")
	}
	peer, e := auth.Bootstrap(ctx, a.DB, "peer-"+fmt.Sprint(time.Now().UnixNano()), "peer", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	save := key
	key = peer
	call(api2.URL, "GET", "/v1/sessions/"+sid, nil, 404)
	call(api2.URL, "GET", "/v1/sessions/"+sid+"/messages", nil, 404)
	call(api2.URL, "GET", "/v1/sessions/"+sid+"/required_actions", nil, 404)
	call(api2.URL, "GET", "/v1/tasks/"+tid+"/generations", nil, 404)
	key = save
	env := call(api1.URL, "POST", "/v1/environments", map[string]any{"name": "local workspace"}, 201)
	writer := call(api1.URL, "POST", "/v1/agents", map[string]any{"name": "writer", "model": "writer", "tools": []any{map[string]any{"name": "write", "kind": "builtin"}}}, 201)
	s2 := call(api1.URL, "POST", "/v1/sessions", map[string]any{"title": "with workspace", "environment_id": env["id"]}, 201)
	t2 := call(api1.URL, "POST", "/v1/sessions/"+s2["id"].(string)+"/tasks", map[string]any{"agent_id": writer["id"], "input": "write output"}, 202)
	until := time.Now().Add(8 * time.Second)
	completed := false
	for time.Now().Before(until) {
		out = call(api2.URL, "GET", "/v1/tasks/"+t2["id"].(string), nil, 200)
		if out["state"] == "succeeded" {
			completed = true
			break
		}
		if execution.Terminal(out["state"].(string)) || out["state"] == "unknown" {
			t.Fatalf("sandbox task: %+v", out)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !completed {
		t.Fatal("sandbox execution timed out")
	}
	rows := call(api2.URL, "GET", "/v1/files?session_id="+s2["id"].(string)+"&task_id="+t2["id"].(string), nil, 200)["data"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["task_id"] != t2["id"] {
		t.Fatal("artifact task attribution", rows)
	}
	if unrelated := call(api2.URL, "GET", "/v1/files?task_id="+tid, nil, 200)["data"].([]any); len(unrelated) != 0 {
		t.Fatal("artifact filter ignored", unrelated)
	}
	key = peer
	if foreign := call(api2.URL, "GET", "/v1/files?task_id="+t2["id"].(string), nil, 200)["data"].([]any); len(foreign) != 0 {
		t.Fatal("artifact ownership leak", foreign)
	}
	key = save
}
