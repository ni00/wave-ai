// Package fixture provides the shared HTTP behavior fixture for every client.
package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

type server struct {
	values      map[string]json.RawMessage
	key, binary string
	mu          sync.Mutex
	calls       map[[3]string]int
}

// New loads the language-neutral fixtures. Each client/case has isolated counters.
func New(path string) (http.Handler, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &server{calls: make(map[[3]string]int)}
	if err = json.Unmarshal(data, &s.values); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(s.values["key"], &s.key); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(s.values["binary"], &s.binary); err != nil {
		return nil, err
	}
	return s, nil
}

func send(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	var body []byte
	if value != nil {
		body, _ = json.Marshal(value)
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		send(w, 404, nil)
		return
	}
	client, scenario, path := parts[0], parts[1], "/"+strings.Join(parts[2:], "/")
	key := [3]string{client, scenario, path}
	s.mu.Lock()
	count := s.calls[key]
	s.calls[key]++
	stats := map[string]int{}
	if path == "/stats" {
		for k, n := range s.calls {
			if k[0] == client && k[1] == scenario && k[2] != "/stats" {
				stats[k[2]] = n
			}
		}
	}
	s.mu.Unlock()
	if path == "/stats" {
		send(w, 200, stats)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.key {
		send(w, 401, s.values["error"])
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		send(w, 413, nil)
		return
	}
	query := r.URL.Query()
	switch {
	case scenario == "error":
		send(w, 403, s.values["error"])
	case scenario == "retry" || strings.HasPrefix(scenario, "no-retry"):
		if scenario == "retry" && (r.Header.Get("Idempotency-Key") == "" || !sameJSON(body, []byte(`{"agent_id":"agent_test","input":"hello"}`))) {
			send(w, 422, map[string]any{"error": map[string]string{"message": "idempotency body changed"}})
			return
		}
		status := 202
		if count == 0 {
			status = 503
		}
		w.Header().Set("Retry-After", "0")
		send(w, status, s.values["task"])
	case scenario == "approve" || scenario == "empty":
		name := "approval"
		if scenario == "empty" {
			name = "emptyResult"
		}
		status := 422
		if sameJSON(body, s.values[name]) {
			status = 202
		}
		send(w, status, nil)
	case scenario == "offset" || scenario == "after" || scenario == "sequence" || scenario == "stuck":
		if scenario == "stuck" {
			send(w, 200, map[string]any{"data": []any{map[string]string{"id": "a"}}, "next_offset": 0})
			return
		}
		field := "after"
		if scenario == "offset" {
			field = "offset"
		}
		cursor := query.Get(field)
		page := map[string]any{"data": []any{}}
		if cursor == "" || cursor == "0" {
			page["data"] = []any{map[string]any{"id": "a", "sequence": 2}}
			if scenario == "offset" {
				page["next_offset"] = 2
			}
			if scenario == "after" {
				page["next_after"] = 2
			}
		}
		send(w, 200, page)
	case strings.HasPrefix(scenario, "wait"):
		if path == "/v1/tasks/task_target" {
			var task map[string]any
			_ = json.Unmarshal(s.values["task"], &task)
			if scenario == "wait-child" && count > 0 {
				task["state"] = "succeeded"
			}
			send(w, 200, task)
			return
		}
		task := "task_child"
		if scenario == "wait-action" {
			task = "task_target"
		}
		send(w, 200, map[string]any{"data": []any{map[string]string{"task_id": task, "type": "approve_tool", "call_id": "call_test"}}})
	case scenario == "sse" || scenario == "reconnect":
		expected := "42"
		if count > 0 {
			expected = "44"
		}
		if query.Has("after") || r.Header.Get("Last-Event-ID") != expected {
			send(w, 422, map[string]any{"error": map[string]string{"message": "wrong resume cursor"}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		payload := "\ufeff: heartbeat\r\nid: 43\r\nevent: task.finished\r\ndata: {\"task_id\":\"task_child\"}\r\n\r\nid: 44\nevent: custom.future\ndata: {\"task_id\": \"task_target\",\ndata: \"text\": \"中文\"}\n\n"
		if count > 0 {
			payload = "id: 45\nevent: task.finished\ndata: {\"task_id\":\"task_target\"}\n\n"
		}
		data := []byte(payload)
		for i := 0; i < len(data); i += 7 {
			if _, err := w.Write(data[i:min(i+7, len(data))]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if scenario == "sse" {
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
			case <-timer.C:
			}
		}
	case scenario == "download" || scenario == "range" || scenario == "not-modified" || scenario == "range-error" || scenario == "json-file":
		if scenario == "not-modified" {
			send(w, 304, nil)
			return
		}
		if scenario == "json-file" {
			send(w, 200, s.values["error"])
			return
		}
		data, status := []byte(s.binary), 200
		if scenario == "range" {
			if r.Header.Get("Range") != "bytes=0-3" {
				send(w, 422, nil)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-3/%d", len(data)))
			data, status = data[:4], 206
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if scenario == "range-error" {
			data, status = []byte("invalid range"), 416
			w.Header().Set("Content-Type", "text/plain")
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(status)
		_, _ = w.Write(data)
	case scenario == "upload":
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") || !bytes.Contains(body, []byte(s.binary)) {
			send(w, 422, map[string]any{"error": map[string]string{"message": "invalid multipart upload"}})
			return
		}
		send(w, 201, map[string]string{"id": "file_test"})
	default:
		send(w, 200, map[string]any{"data": []any{}, "usage": s.values["usage"]})
	}
}
