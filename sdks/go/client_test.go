package wave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/ni00/wave-ai/sdks/go/generated"
)

func fixture(t *testing.T, scenario string) *Client {
	t.Helper()
	base := os.Getenv("WAVE_CLIENT_TEST_URL")
	if base == "" {
		t.Skip("run make clients-test for shared HTTP fixtures")
	}
	c, e := New(base+"/go/"+scenario, "Bearer wave-test-key")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestSerialization(t *testing.T) {
	var usage generated.ModelclientUsage
	if e := json.Unmarshal([]byte(`{"cache_read_input_tokens":null,"completion_tokens":null,"prompt_tokens":null}`), &usage); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(usage)
	if !bytes.Contains(b, []byte(`"prompt_tokens":null`)) {
		t.Fatal(string(b))
	}
	if e := validateTool(map[string]any{"approve": false}); e != nil {
		t.Fatal(e)
	}
	if e := validateTool(map[string]any{"result": ""}); e != nil {
		t.Fatal(e)
	}
	if e := validateTool(map[string]any{"approve": false, "result": ""}); e == nil {
		t.Fatal("accepted mutually exclusive fields")
	}
}
func TestErrorAndRawAuth(t *testing.T) {
	c := fixture(t, "error")
	_, e := c.Call(context.Background(), "agentsList", Options{})
	var api *APIError
	if !errors.As(e, &api) || api.RequestID != "req_contract" || api.Code != "permission_error" {
		t.Fatalf("%#v", e)
	}
	_, response, e := c.Raw().AgentsAPI.AgentsList(context.Background()).Execute()
	if e == nil || response.StatusCode != 403 {
		t.Fatal("raw auth", e, response)
	}
}
func TestExplicitValues(t *testing.T) {
	if e := fixture(t, "approve").Approve(context.Background(), "task", "call", false); e != nil {
		t.Fatal(e)
	}
	if e := fixture(t, "empty").SubmitResult(context.Background(), "task", "call", "", false, ""); e != nil {
		t.Fatal(e)
	}
}
func TestRetry(t *testing.T) {
	for _, scenario := range []string{"retry", "no-retry"} {
		c := fixture(t, scenario)
		o := Options{Path: map[string]string{"id": "session"}, Body: map[string]any{"agent_id": "agent_test", "input": "hello"}, Headers: make(http.Header)}
		if scenario == "retry" {
			o.Headers.Set("Idempotency-Key", "stable-key")
		}
		_, e := c.Call(context.Background(), "executionCreateTask", o)
		if (e == nil) != (scenario == "retry") {
			t.Fatal(scenario, e)
		}
	}
}
func TestPagination(t *testing.T) {
	for scenario, op := range map[string]string{"offset": "agentsList", "after": "executionMessages", "sequence": "executionListEvents", "stuck": "agentsList"} {
		c := fixture(t, scenario)
		n := 0
		e := c.Each(context.Background(), op, Options{Path: map[string]string{"id": "session"}}, func(json.RawMessage) error { n++; return nil })
		if scenario == "stuck" {
			if e == nil {
				t.Fatal("nonadvancing cursor accepted")
			}
		} else if e != nil || n != 1 {
			t.Fatal(scenario, n, e)
		}
	}
}
func TestSSEFirstFrameAndReconnect(t *testing.T) {
	for _, scenario := range []string{"sse", "reconnect"} {
		c := fixture(t, scenario)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sentinel := errors.New("done")
		n := 0
		e := c.Events(ctx, "session", StreamOptions{LastEventID: "42", TaskID: "task_target", MaxReconnects: 1}, func(ev Event) error {
			n++
			if n == 1 && (ev.ID != "44" || ev.Type != "custom.future") {
				t.Fatal(ev)
			}
			if scenario == "sse" || n == 2 {
				return sentinel
			}
			return nil
		})
		if !errors.Is(e, sentinel) {
			t.Fatal(scenario, n, e)
		}
	}
}
func TestWaitFiltersChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, e := fixture(t, "wait-child").Wait(ctx, "task_target", time.Millisecond)
	if e != nil || r.Reason != "terminal" {
		t.Fatal(r, e)
	}
	r, e = fixture(t, "wait-action").Wait(ctx, "task_target", time.Millisecond)
	if e != nil || r.Reason != "action" || len(r.RequiredActions) != 1 {
		t.Fatal(r, e)
	}
}
func TestFiles(t *testing.T) {
	for _, scenario := range []string{"download", "range", "not-modified", "range-error", "json-file"} {
		c := fixture(t, scenario)
		var b bytes.Buffer
		headers := http.Header{"Range": []string{"bytes=0-3"}}
		r, e := c.Download(context.Background(), "filesContent", "file", headers, &b)
		if scenario == "range-error" || scenario == "json-file" {
			if e == nil || b.Len() != 0 {
				t.Fatal(scenario, e, b.String())
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		if scenario == "not-modified" && b.Len() != 0 {
			t.Fatal(b.String())
		}
		if scenario == "range" && (r.StatusCode != 206 || b.String() != "Wave") {
			t.Fatal(r, b.String())
		}
	}
	_, e := fixture(t, "upload").Upload(context.Background(), "filesUpload", "中文.txt", bytes.NewBufferString("Wave artifact\n中文内容\n"), nil)
	if e != nil {
		t.Fatal(e)
	}
}
func TestPathAndCursor(t *testing.T) {
	c, _ := New("http://localhost", "")
	op := Operations()["executionStreamEvents"]
	r, e := c.request(context.Background(), op, Options{Path: map[string]string{"id": "a/b"}, Headers: http.Header{"Last-Event-Id": []string{"42"}}, Query: url.Values{}}, nil, "")
	if e != nil || r.URL.RawQuery != "" || r.URL.EscapedPath() != "/v1/sessions/a%2Fb/events/stream" {
		t.Fatal(r, e)
	}
}
