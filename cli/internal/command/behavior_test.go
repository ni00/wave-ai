package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypedBodyAndDryRun(t *testing.T) {
	cases := []struct {
		args  []string
		field string
		want  any
	}{
		{[]string{"agents", "create", "--name", "demo", "--model", "test", "--skill-id", "s1", "--skill-id", "s2"}, "skill_ids", []any{"s1", "s2"}},
		{[]string{"deployments", "pause", "dep", "--paused=false"}, "paused", false},
		{[]string{"tools", "resolve", "task", "call", "--result", ""}, "result", ""},
		{[]string{"tasks", "create", "--session", "s", "--agent", "a", "--input", "hello"}, "agent_id", "a"},
	}
	for _, tc := range cases {
		args := append(tc.args, "--dry-run")
		code, out, err := invoke(t, "", "", args...)
		if code != 0 {
			t.Fatal(args, code, err)
		}
		var preview struct {
			Body    map[string]any      `json:"body"`
			Headers map[string][]string `json:"headers"`
		}
		if e := json.Unmarshal([]byte(out), &preview); e != nil {
			t.Fatal(e)
		}
		want, _ := json.Marshal(tc.want)
		got, _ := json.Marshal(preview.Body[tc.field])
		if !bytes.Equal(want, got) {
			t.Fatalf("%s != %s", got, want)
		}
		if strings.Contains(out, "wave-test-key") {
			t.Fatal("dry-run leaked configured API key")
		}
	}
	code, out, err := invoke(t, "", "", "credentials", "create", "--name", "key", "--host", "example.com", "--token", "secret-value", "--dry-run")
	if code != 0 || strings.Contains(out, "secret-value") || !strings.Contains(out, "REDACTED") {
		t.Fatal(code, out, err)
	}
}

func TestInvalidRequestsFailBeforeNetwork(t *testing.T) {
	cases := [][]string{
		{"agents", "create", "--name", "missing-model"},
		{"agents", "create", "--body", `{"name":"a","model":"m","typo":1}`},
		{"tools", "resolve", "task", "call", "--approve=false", "--result", ""},
		{"tools", "result", "call", "--task", "task", "--result", "", "--error-code", "oops"},
		{"tasks", "create", "--session", "s", "--id", "different", "--agent", "a", "--input", "x"},
		{"events", "watch", "s", "--after", "0", "--last-event-id", "42"},
		{"events", "watch", "s", "--pretty"},
		{"agents", "list", "--limit", "201"},
		{"files", "upload", "--file", "-"},
		{"agents", "list", "--raw"},
		{"files", "download", "file", "--output", "-", "--select", "/id"},
	}
	for _, args := range cases {
		code, _, err := invoke(t, "", "", args...)
		if code != 2 {
			t.Fatalf("%v: code %d %s", args, code, err)
		}
	}
}

type unreadableInput struct{ t *testing.T }

func (r unreadableInput) Read([]byte) (int, error) {
	r.t.Fatal("ambiguous input should be rejected before reading stdin")
	return 0, nil
}

func TestAmbiguousBodyDoesNotReadStdin(t *testing.T) {
	for _, args := range [][]string{
		{"agents", "create", "--body", "{}", "--instructions-file", "-"},
		{"agents", "create", "--instructions-file", "-", "--tools", "-"},
	} {
		var out, err bytes.Buffer
		if code := Execute(context.Background(), "test", args, unreadableInput{t}, &out, &err); code != 2 {
			t.Fatal(code, err.String())
		}
	}
}

func TestAgentTableIncludesConfigurationName(t *testing.T) {
	cmd := newRoot("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := table(cmd, []any{map[string]any{"id": "agent_1", "config": map[string]any{"name": "research", "model": "test-model"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "research") || !strings.Contains(out.String(), "test-model") {
		t.Fatal(out.String())
	}
}

func TestBoundedTextInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(file, bytes.Repeat([]byte("a"), maxJSONBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"tasks", "create", "--session", "s", "--agent", "a", "--input-file", file}, {"tools", "result", "call", "--task", "task", "--result-file", "-"}} {
		code, _, _ := invoke(t, "", strings.Repeat("x", maxJSONBytes+1), args...)
		if code != 2 {
			t.Fatal(args, code)
		}
	}
}

func TestFocusedSchemaAndSelection(t *testing.T) {
	code, out, err := invoke(t, "", "", "schema", "tasks", "create", "--request")
	if code != 0 || !strings.Contains(out, "execution.TaskRequest") || strings.Contains(out, "files.File") {
		t.Fatal(code, out, err)
	}
	code, out, err = invoke(t, "", "", "schema", "agents", "create", "--example", "--select", "/model", "--raw")
	if code != 0 || strings.TrimSpace(out) != "your-model-id" {
		t.Fatal(code, out, err)
	}
	code, _, _ = invoke(t, "", "", "schema", "agents", "create", "--example", "--select", "/missing")
	if code != 2 {
		t.Fatal(code)
	}
	code, out, err = invoke(t, "", "", "doctor", "--offline", "--format", "table")
	if code != 0 || !strings.Contains(out, "url") || strings.Contains(out, "wave-test-key") {
		t.Fatal(code, out, err)
	}
}

func TestProfileMigrationAndSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("WAVE_CONFIG_FILE", path)
	t.Setenv("WAVE_PROFILE", "")
	t.Setenv("WAVE_BASE_URL", "")
	t.Setenv("WAVE_API_KEY", "")
	if err := os.WriteFile(path, []byte(`{"local":{"url":"http://localhost:8080","api_key":"private-key"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		var out, err bytes.Buffer
		code := Execute(context.Background(), "test", args, strings.NewReader(""), &out, &err)
		if code != 0 {
			t.Fatal(args, code, err.String())
		}
		if strings.Contains(out.String(), "private-key") {
			t.Fatal("leaked key")
		}
		return out.String()
	}
	run("config", "use", "local")
	if got := run("config", "show", "--effective"); !strings.Contains(got, `"profile":"local"`) {
		t.Fatal(got)
	}
	data, _ := os.ReadFile(path)
	var store profileStore
	if err := json.Unmarshal(data, &store); err != nil || store.Profiles["local"].Key != "private-key" || store.Active != "local" {
		t.Fatal(store, err)
	}
	t.Setenv("WAVE_BASE_URL", "http://localhost:9090")
	if got := run("config", "show", "--effective"); !strings.Contains(got, `"url_source":"environment"`) {
		t.Fatal(got)
	}
	run("config", "unset-key", "local")
	run("config", "remove", "local")
	var out, err bytes.Buffer
	if code := Execute(context.Background(), "test", []string{"doctor", "--offline", "--profile", "missing"}, strings.NewReader(""), &out, &err); code != 2 {
		t.Fatal(code, err.String())
	}
}

func TestEventCheckpointAndStdinUpload(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.HasSuffix(r.URL.Path, "/stream") {
			if r.Header.Get("Last-Event-ID") != "42" || r.URL.Query().Has("after") {
				t.Error("wrong cursor", r.URL, r.Header)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 43\nevent: custom\ndata: {\"task_id\":\"task\"}\n\n")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		if header.Filename != "report.txt" {
			t.Error(header.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"file_test"}`)
	}))
	defer server.Close()
	cursor := filepath.Join(t.TempDir(), "cursor.json")
	if err := writeCheckpoint(cursor, "s", "42"); err != nil {
		t.Fatal(err)
	}
	code, out, err := invoke(t, "", "", "--url", server.URL, "events", "watch", "s", "--cursor-file", cursor)
	if code != 0 || !strings.Contains(out, `"id":"43"`) {
		t.Fatal(code, out, err)
	}
	if next, err := readCheckpoint(cursor, "s"); err != nil || next != "43" {
		t.Fatal(next, err)
	}
	code, _, _ = invoke(t, "", "", "--url", server.URL, "events", "watch", "other", "--cursor-file", cursor)
	if code != 2 || requests != 1 {
		t.Fatal("cross-session checkpoint reached server", code, requests)
	}
	code, out, err = invoke(t, "", "contents", "--url", server.URL, "files", "upload", "--file", "-", "--filename", "report.txt")
	if code != 0 || !strings.Contains(out, "file_test") {
		t.Fatal(code, out, err)
	}
}

func TestCreateAndWaitKeepsTaskOnTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			w.WriteHeader(202)
			fmt.Fprint(w, `{"id":"task_created"}`)
			return
		}
		if strings.Contains(r.URL.Path, "required_actions") {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		fmt.Fprint(w, `{"id":"task_created","session_id":"s","state":"running"}`)
	}))
	defer server.Close()
	code, _, err := invoke(t, "", "", "--url", server.URL, "tasks", "create", "--session", "s", "--agent", "a", "--input", "hello", "--wait", "--timeout", "40ms")
	if code != 5 || !strings.Contains(err, "task_created") || !strings.Contains(err, "wavectl tasks wait") {
		t.Fatal(code, err)
	}
}
