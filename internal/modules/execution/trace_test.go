package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/observe"
)

func TestTraceDurationsUnionAndPrivacy(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(ms int) *time.Time { v := start.Add(time.Duration(ms) * time.Millisecond); return &v }
	task := Task{ID: "task", RootID: "task", SessionID: "session", CreatedAt: start, StartedAt: at(10), FinishedAt: at(100), State: "succeeded", UsageKnown: true, Result: "SECRET-output", Error: "SECRET-error"}
	n := int64(3)
	gs := []Generation{{ID: "gen", TaskID: "task", Purpose: "response", State: "completed", StartedAt: *at(30), FinishedAt: at(60), Usage: modelclient.Usage{PromptTokens: &n, CompletionTokens: &n}, Error: "SECRET-model-error"}}
	cs := []ToolCall{{ID: "call", TaskID: "task", StartedAt: at(65), FinishedAt: at(70), Status: "completed", Arguments: "SECRET-command", Result: "SECRET-result"}}
	event := func(id, parent, name string, from, to int) Event {
		span := observe.Span{ID: id, ParentID: parent, TaskID: "task", Kind: "phase", Name: name, State: "completed", StartedAt: *at(from), FinishedAt: at(to), DurationMS: duration(*at(from), at(to))}
		b, _ := json.Marshal(span)
		data := map[string]any{}
		json.Unmarshal(b, &data)
		return Event{Data: data}
	}
	events := []Event{event("prepare", "task", "workspace.prepare", 10, 25), event("ensure", "prepare", "sandbox.ensure", 12, 20), event("finish", "task", "workspace.finish", 80, 90)}
	r := buildTrace(task, gs, cs, events, []Task{{ID: "child", ParentID: "task", CreatedAt: *at(5), FinishedAt: at(95)}})
	if r.Incomplete || *r.Summary.WallMS != 100 || *r.Summary.UnattributedMS != 30 || r.Summary.ModelMS != 30 || r.Summary.ToolMS != 5 || r.Summary.PhaseMS["workspace.prepare"] != 15 || len(r.Summary.PhaseMS) != 2 {
		t.Fatalf("%+v", r)
	}
	if r.Summary.InputTokens != 3 || r.Summary.OutputTokens != 3 {
		t.Fatal(r.Summary)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("trace leaked content")
	}
	gs[0].FinishedAt = nil
	gs[0].State = "interrupted"
	gs[0].Usage = modelclient.Usage{}
	r = buildTrace(task, gs, cs, events, nil)
	if !r.Incomplete || r.Summary.UnattributedMS != nil || r.Summary.UsageKnown || r.Summary.FailedModelCalls != 1 {
		t.Fatalf("unknown duration became success: %+v", r)
	}
}

func TestDurablePhaseAndTraceOwnership(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "answer", nil)
	created := task(t, db, p, s, a)
	claimed, err := claimWithAdmission(context.Background(), db, "trace-test", time.Minute, nil)
	if err != nil || claimed == nil || claimed.ID != created.ID {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	w := Worker{DB: db}
	ctx := w.traceContext(context.Background(), claimed)
	if err := observe.Do(ctx, "workspace.prepare", func(ctx context.Context) error {
		return observe.Do(ctx, "sandbox.ensure", func(context.Context) error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	r, err := TaskTrace(ctx, db, p, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Spans) != 4 || r.TraceID != created.ID || !r.Incomplete {
		t.Fatalf("%+v", r)
	}
	other := *p
	other.PrincipalID = "00000000-0000-0000-0000-000000000001"
	if _, err = TaskTrace(ctx, db, &other, created.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-owner trace: %v", err)
	}
	other = auth.Principal{OrgID: "00000000-0000-0000-0000-000000000002", PrincipalID: p.PrincipalID}
	if _, err = TaskTrace(ctx, db, &other, created.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-org trace: %v", err)
	}
	// A lost fence must not replay or fail the business operation for telemetry.
	claimed.Epoch++
	called := false
	if err = observe.Do(ctx, "sandbox.stop", func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatal("telemetry affected operation")
	}
	r, err = TaskTrace(ctx, db, p, created.ID)
	if err != nil || len(r.Spans) != 4 {
		t.Fatal("unfenced telemetry persisted", err, r)
	}
}
