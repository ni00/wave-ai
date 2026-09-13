package wave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRealService(t *testing.T) {
	if os.Getenv("WAVE_CLIENT_E2E") == "" {
		t.Skip("run make clients-integration")
	}
	data, e := os.ReadFile("../../tests/clients/workflow.json")
	if e != nil {
		t.Fatal(e)
	}
	var f map[string]any
	if e = json.Unmarshal(data, &f); e != nil {
		t.Fatal(e)
	}
	c, e := New(os.Getenv("WAVE_BASE_URL"), os.Getenv("WAVE_API_KEY"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := func(op string, path map[string]string, body any) map[string]any {
		t.Helper()
		r, e := c.Call(ctx, op, Options{Path: path, Body: body})
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		if len(r.Data) > 0 {
			if e = json.Unmarshal(r.Data, &out); e != nil {
				t.Fatal(e)
			}
		}
		return out
	}
	agent := call("agentsCreate", nil, f["agent"])
	session := call("executionCreateSession", nil, f["session"])
	sid := session["id"].(string)
	task := call("executionCreateTask", map[string]string{"id": sid}, map[string]any{"agent_id": agent["id"], "input": f["input"]})
	tid := task["id"].(string)
	wait := func() *WaitResult {
		t.Helper()
		r, e := c.Wait(ctx, tid, 10*time.Millisecond)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	action := wait()
	if action.Reason != "action" || action.RequiredActions[0]["type"] != "approve_tool" {
		t.Fatal(action)
	}
	callID := action.RequiredActions[0]["call_id"].(string)
	if e = c.Approve(ctx, tid, callID, true); e != nil {
		t.Fatal(e)
	}
	action = wait()
	if action.RequiredActions[0]["type"] != "submit_tool_result" {
		t.Fatal(action)
	}
	if e = c.SubmitResult(ctx, tid, callID, "", false, ""); e != nil {
		t.Fatal(e)
	}
	result := wait()
	if result.Task["state"] != "succeeded" {
		t.Fatal(result)
	}
	typed, _, e := c.Raw().ExecutionAPI.ExecutionGetTask(ctx, tid).Execute()
	if e != nil || typed.State != "succeeded" {
		t.Fatal(typed, e)
	}
	stop := errors.New("first event received")
	e = c.Events(ctx, sid, StreamOptions{LastEventID: "0", TaskID: tid}, func(ev Event) error {
		if ev.ID == "" {
			t.Fatal(ev)
		}
		return stop
	})
	if !errors.Is(e, stop) {
		t.Fatal(e)
	}
	upload, e := c.Upload(ctx, "filesUpload", "中文.txt", bytes.NewBufferString(f["file"].(string)), nil)
	if e != nil {
		t.Fatal(e)
	}
	var file map[string]any
	json.Unmarshal(upload.Data, &file)
	var downloaded bytes.Buffer
	if _, e = c.Download(ctx, "filesContent", file["id"].(string), nil, &downloaded); e != nil || downloaded.String() != f["file"] {
		t.Fatal(downloaded.String(), e)
	}
}
