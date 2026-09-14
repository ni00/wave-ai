package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"wave-ai.local/wave/internal/modules/console"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/telemetry"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestMonitorAPIIsolationSearchAndTimeLinks(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	if e := Init(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	a, e := New(ctx, storageTestConfig(t, dsn, "local"))
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	org := xid.New("monitor")
	key, e := auth.Bootstrap(ctx, a.DB, org, "alice", "monitor", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	peer, e := auth.Bootstrap(ctx, a.DB, org, "bob", "monitor", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	p, e := auth.Authenticate(ctx, a.DB, key)
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	session := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID}
	if e = a.DB.Create(&session).Error; e != nil {
		t.Fatal(e)
	}
	task := execution.Task{ID: xid.New("task"), SessionID: session.ID, State: "failed", CreatedAt: at.Add(-2 * time.Hour), FinishedAt: &at}
	task.RootID = task.ID
	if e = a.DB.Create(&task).Error; e != nil {
		t.Fatal(e)
	}
	var logs []observe.Log
	for i := 0; i < 5; i++ {
		level := "info"
		if i%2 == 0 {
			level = "error"
		}
		logs = append(logs, observe.Log{ID: xid.New("log"), OrgID: p.OrgID, OwnerID: p.PrincipalID, CreatedAt: at, Level: level, Module: "model", Message: "model.finished", TaskID: task.ID, TraceID: task.ID, SessionID: session.ID, SpanID: "generation_test", Attributes: map[string]any{"state": "failed"}, Error: map[string]any{"error_kind": "deadline_exceeded"}, ErrorText: "deadline_exceeded"})
	}
	if e = a.DB.Create(&logs).Error; e != nil {
		t.Fatal(e)
	}
	if e = telemetry.Enqueue(a.DB, p.OrgID, p.PrincipalID, at, map[string]float64{"tasks.completed": 1, "tasks.failed": 1, "task.duration": 7200000}); e != nil {
		t.Fatal(e)
	}
	for {
		n, err := telemetry.Reduce(ctx, a.DB)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	h := a.Handler()
	get := func(key, path string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	get("", "/v1/console/metrics", 401)
	get(peer, "/v1/console/logs/"+logs[0].ID, 404)
	var list console.List
	if e = json.Unmarshal(get(peer, "/v1/console/resources/logs?task_id="+task.ID, 200), &list); e != nil || len(list.Data) != 0 {
		t.Fatal("log owner isolation", list, e)
	}
	get(key, "/v1/console/resources/logs?state=madeup", 400)
	get(key, "/v1/console/resources/logs?cursor=bad", 400)
	get(key, "/v1/console/metrics?from=bad", 400)
	get(key, "/v1/console/metrics?from=2020-01-01T00:00:00Z", 400)
	if e = json.Unmarshal(get(key, "/v1/console/resources/logs?q=DEADLINE&state=error&module=model&span_id=generation_test", 200), &list); e != nil || len(list.Data) != 3 {
		t.Fatal("log filters", list, e)
	}
	seen := map[string]bool{}
	cursor := ""
	for range 4 {
		list = console.List{}
		if e = json.Unmarshal(get(key, "/v1/console/resources/logs?task_id="+task.ID+"&limit=2&cursor="+cursor, 200), &list); e != nil {
			t.Fatal(e)
		}
		for _, l := range list.Data {
			if seen[l.ID] {
				t.Fatal("duplicate log")
			}
			seen[l.ID] = true
		}
		cursor = list.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatal("log page loss", seen)
	}
	var detail observe.Log
	if e = json.Unmarshal(get(key, "/v1/console/logs/"+logs[0].ID, 200), &detail); e != nil || detail.SpanID != "generation_test" || detail.Error["error_kind"] != "deadline_exceeded" {
		t.Fatal(detail, e)
	}
	interval := "after=" + at.Format(time.RFC3339) + "&before=" + at.Add(time.Minute).Format(time.RFC3339)
	if e = json.Unmarshal(get(key, "/v1/console/resources/tasks?state=failed&time_field=finished_at&"+interval, 200), &list); e != nil || len(list.Data) != 1 || list.Data[0].ID != task.ID {
		t.Fatal("completion-time link", list, e)
	}
	if e = json.Unmarshal(get(key, "/v1/console/resources/tasks?"+interval, 200), &list); e != nil || len(list.Data) != 0 {
		t.Fatal("creation time unexpectedly matches", list, e)
	}
	var metrics telemetry.Metrics
	if e = json.Unmarshal(get(key, "/v1/console/metrics", 200), &metrics); e != nil || metrics.Totals["tasks.failed"].Sum != 1 {
		t.Fatal(metrics, e)
	}
	var other telemetry.Metrics
	if e = json.Unmarshal(get(peer, "/v1/console/metrics", 200), &other); e != nil || len(other.Totals) != 0 {
		t.Fatal("metrics owner isolation", other, e)
	}
}
