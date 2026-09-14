package execution

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestWaitSelectionAndDeadline(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "answer", nil)
	root := task(t, db, p, s, a)
	children := []Task{}
	for i := 0; i < 2; i++ {
		c := Task{ID: xid.New("task"), SessionID: s.ID, RootID: root.ID, ParentID: root.ID, State: "running", AgentID: a.ID, AgentVersion: 1, Snapshot: a.Config, Budget: Budget{}.Defaults()}
		if e := db.Create(&c).Error; e != nil {
			t.Fatal(e)
		}
		children = append(children, c)
	}
	w := Worker{DB: db}
	run := func(args string, started *time.Time) ToolCall {
		t.Helper()
		until := time.Now().Add(time.Minute)
		root.State = "running"
		root.Owner = "wait-test"
		root.LeaseUntil = &until
		root.Epoch++
		if e := db.Save(&root).Error; e != nil {
			t.Fatal(e)
		}
		call := ToolCall{ID: xid.New("call"), TaskID: root.ID, ModelID: xid.New("model"), Tool: agents.Tool{Name: "agent_wait"}, Arguments: args, Status: "ready", StartedAt: started}
		if e := db.Create(&call).Error; e != nil {
			t.Fatal(e)
		}
		if e := w.waitChildren(ctx, &root, call); e != nil {
			t.Fatal(e)
		}
		if e := db.Where(eq("id", call.ID)).Take(&call).Error; e != nil {
			t.Fatal(e)
		}
		return call
	}
	if e := db.Model(&children[0]).Update("state", "succeeded").Error; e != nil {
		t.Fatal(e)
	}
	call := run(`{"mode":"any"}`, nil)
	if call.Status != "completed" {
		t.Fatal("any did not complete")
	}
	call = run(`{"mode":"all"}`, nil)
	if call.Status != "waiting_children" {
		t.Fatal("all failed to park")
	}
	selected, _ := json.Marshal(waitRequest{TaskIDs: []string{children[0].ID}, Mode: "all"})
	call = run(string(selected), nil)
	if call.Status != "completed" {
		t.Fatal("selected task did not complete")
	}
	call = run(`{"task_ids":["outside"]}`, nil)
	if !call.IsError || call.Status != "completed" {
		t.Fatal("foreign task selection accepted")
	}
	old := time.Now().Add(-2 * time.Second)
	call = run(`{"mode":"all","timeout_seconds":1}`, &old)
	if call.Status != "completed" {
		t.Fatal("expired wait not completed")
	}
	var result struct {
		TimedOut bool `json:"timed_out"`
	}
	json.Unmarshal([]byte(call.Result), &result)
	if !result.TimedOut {
		t.Fatal("missing timeout indication")
	}
	call = run(`{"timeout_seconds":10}`, nil)
	if call.WaitUntil == nil {
		t.Fatal("deadline not persisted")
	}
	if e := db.Model(&call).Update("wait_until", old).Error; e != nil {
		t.Fatal(e)
	}
	if e := WakeWaiters(ctx, db); e != nil {
		t.Fatal(e)
	}
	var current Task
	db.Where(eq("id", root.ID)).Take(&current)
	if current.State != "queued" {
		t.Fatal("deadline did not wake coordinator")
	}
}

func TestFinishPersistsIndependentEvaluation(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	root := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "evaluator", time.Minute)
	if e != nil || claim == nil || claim.ID != root.ID {
		t.Fatalf("claim: %v %v", claim, e)
	}
	calls := 0
	w := Worker{DB: db, Evaluate: func(context.Context, Session, Task) *Evaluation {
		calls++
		return &Evaluation{Status: "failed", Checks: []CheckResult{{Kind: "json", Status: "failed", Error: "missing field"}}, EvaluatedAt: time.Now()}
	}}
	if e = w.finish(ctx, claim, "succeeded"); e != nil {
		t.Fatal(e)
	}
	var current Task
	db.Where(eq("id", root.ID)).Take(&current)
	if calls != 1 || current.State != "succeeded" || current.Evaluation == nil || current.Evaluation.Status != "failed" {
		t.Fatalf("evaluation changed execution state: %+v", current)
	}
}
