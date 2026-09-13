package execution

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestAdmissionAndPinnedExperts(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	expert := agent(t, db, p, "version one", []agents.Tool{{Kind: "builtin", Name: "write"}, {Kind: "builtin", Name: "bash"}})
	a, e := agents.Create(ctx, db, p, agents.Config{Name: "coordinator", Model: "any", Tools: []agents.Tool{{Kind: "builtin", Name: "write", Approval: true}}, ExpertIDs: []string{expert.ID}})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 10)
	for range 10 {
		wg.Go(func() {
			x, e := Admit(ctx, db, p, "same-key", s.ID, "same-body", func(tx *gorm.DB) (Task, error) { return CreateTask(ctx, tx, p, s.ID, a.ID, "input", Budget{}) })
			if e != nil {
				t.Error(e)
				return
			}
			ids <- x.ID
		})
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate admission")
		}
		id = got
	}
	if _, e = Admit(ctx, db, p, "same-key", s.ID, "different", func(tx *gorm.DB) (Task, error) { return Task{}, nil }); e == nil {
		t.Fatal("key body conflict accepted")
	}
	cfg := expert.Config
	cfg.Instructions = "version two"
	if _, e = agents.Update(ctx, db, p, expert.ID, expert.Version, cfg); e != nil {
		t.Fatal(e)
	}
	child, e := Spawn(ctx, db, p, id, expert.ID, "child", Budget{MaxTokens: 10, MaxToolCalls: 2})
	if e != nil {
		t.Fatal(e)
	}
	if child.Snapshot.Instructions == "version two" || !strings.HasPrefix(child.Snapshot.Instructions, "version one") || len(child.Snapshot.Tools) != 1 || !child.Snapshot.Tools[0].Approval || child.Budget.MaxTokens != 10 {
		t.Fatalf("snapshot or permission escalation: %+v", child)
	}
	if _, e = Spawn(ctx, db, p, child.ID, expert.ID, "grandchild"); e == nil {
		t.Fatal("nested delegation accepted")
	}
	now := time.Now()
	if e = db.Model(&Task{}).Where(eq("id", child.ID)).Updates(map[string]any{"state": "succeeded", "finished_at": &now, "used_tokens": 7}).Error; e != nil {
		t.Fatal(e)
	}
	if e = Followup(ctx, db, p, child.ID, "continue"); e != nil {
		t.Fatal(e)
	}
	var resumed Task
	db.Where(eq("id", child.ID)).Take(&resumed)
	if resumed.State != "queued" || resumed.UsedTokens != 7 || resumed.Budget.MaxTokens != 10 {
		t.Fatal("followup reset budget")
	}
}

func TestWaitAndExpiration(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	expert := agent(t, db, p, "", nil)
	a, e := agents.Create(ctx, db, p, agents.Config{Name: "root", Model: "any", ExpertIDs: []string{expert.ID}})
	if e != nil {
		t.Fatal(e)
	}
	root := task(t, db, p, s, a)
	child, e := Spawn(ctx, db, p, root.ID, expert.ID, "child")
	if e != nil {
		t.Fatal(e)
	}
	claim, e := Claim(ctx, db, "test", time.Minute)
	if e != nil || claim == nil || claim.ID != root.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	call := ToolCall{ID: xid.New("call"), TaskID: root.ID, ModelID: "wait", Tool: agents.Tool{Name: "agent_wait", Kind: "agent"}, Status: "ready"}
	if e = db.Create(&call).Error; e != nil {
		t.Fatal(e)
	}
	w := Worker{DB: db}
	if e = w.waitChildren(ctx, claim, call); e != nil {
		t.Fatal(e)
	}
	waitState(t, db, root.ID, "waiting")
	cc, e := Claim(ctx, db, "child", time.Minute)
	if e != nil || cc == nil || cc.ID != child.ID {
		t.Fatalf("child claim %v %v", cc, e)
	}
	if e = w.finish(ctx, cc, "succeeded"); e != nil {
		t.Fatal(e)
	}
	waitState(t, db, root.ID, "queued")
	claim, e = Claim(ctx, db, "root", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	db.Where(eq("id", call.ID)).Take(&call)
	if e = w.waitChildren(ctx, claim, call); e != nil {
		t.Fatal(e)
	}
	db.Where(eq("id", call.ID)).Take(&call)
	if call.Status != "completed" || !strings.Contains(call.Result, "succeeded") {
		t.Fatal(call)
	}
	started := time.Now().Add(-time.Hour)
	if e = db.Model(&Task{}).Where(eq("id", root.ID)).Updates(map[string]any{"state": "waiting", "started_at": started}).Error; e != nil {
		t.Fatal(e)
	}
	if e = Expire(ctx, db); e != nil {
		t.Fatal(e)
	}
	expired := waitState(t, db, root.ID, "queued")
	if !expired.CancelRequested || expired.PendingFinish != "failed" {
		t.Fatal(expired)
	}
}

func TestModelRetryAndCancellation(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "test", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	if e = db.Model(&Task{}).Where(eq("id", x.ID)).Update("attempts", 10).Error; e != nil {
		t.Fatal(e)
	}
	w := Worker{DB: db, Model: modelFunc(func(context.Context, modelclient.Request, func(modelclient.Delta)) (*modelclient.Final, error) {
		return nil, errors.New("temporary outage")
	})}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	row := waitState(t, db, x.ID, "queued")
	if row.Failures != 1 || row.PendingFinish != "" {
		t.Fatal("total attempts incorrectly exhausted retries")
	}
	claim, e = Claim(ctx, db, "retry", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	w.Model = fakeModel{}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	if e = db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.Failures != 0 {
		t.Fatal("success did not reset consecutive failures")
	}
	if e = Cancel(ctx, db, p, x.ID); e != nil {
		t.Fatal(e)
	}
	call := ToolCall{ID: xid.New("call"), TaskID: x.ID, Status: "approval"}
	if e = db.Create(&call).Error; e != nil {
		t.Fatal(e)
	}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	waitState(t, db, x.ID, "canceled")
	db.Where(eq("id", call.ID)).Take(&call)
	if call.Status != "completed" || !call.IsError {
		t.Fatal("canceled approval remains executable")
	}
}

type modelFunc func(context.Context, modelclient.Request, func(modelclient.Delta)) (*modelclient.Final, error)

func (f modelFunc) Stream(c context.Context, r modelclient.Request, d func(modelclient.Delta)) (*modelclient.Final, error) {
	return f(c, r, d)
}

func TestResultCommitFailureStopsProgress(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "test", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	call := ToolCall{ID: xid.New("call"), TaskID: x.ID, ModelID: "write", Tool: agents.Tool{Name: "write", Kind: "builtin"}, Status: "ready"}
	if e = db.Create(&call).Error; e != nil {
		t.Fatal(e)
	}
	name := "test:result-failure"
	e = db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if c, ok := tx.Statement.Dest.(*ToolCall); ok && c.Status == "completed" {
			tx.AddError(errors.New("injected result commit failure"))
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Callback().Update().Remove(name)
	w := Worker{DB: db, Execute: func(context.Context, Session, ToolCall) (ToolResult, error) {
		return ToolResult{Output: "effect happened"}, nil
	}}
	if e = w.tool(ctx, claim, s, call); e == nil {
		t.Fatal("commit failure swallowed")
	}
	var persisted ToolCall
	db.Where(eq("id", call.ID)).Take(&persisted)
	if persisted.Status != "running" || persisted.Delivered {
		t.Fatal("uncommitted output advanced")
	}
	db.Model(&Task{}).Where(eq("id", x.ID)).Update("lease_until", time.Now().Add(-time.Second))
	if c, e := Claim(ctx, db, "recovery", time.Minute); e != nil || c != nil {
		t.Fatalf("unknown effect replayed: %v %v", c, e)
	}
	waitState(t, db, x.ID, "unknown")
}

func TestCompleteHistory(t *testing.T) {
	m := []modelclient.Message{{Role: "assistant", ToolCalls: []modelclient.ToolCall{{ID: "a"}, {ID: "b"}}}, {Role: "tool", ToolCallID: "a", Content: "known"}, {Role: "user", Content: "continue"}}
	got := completeHistory(m)
	if len(got) != 4 || got[2].ToolCallID != "b" || got[3].Role != "user" {
		t.Fatal(got)
	}
}

func TestSteeringWaitsForToolBoundary(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "test", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	if e := db.Model(&Input{}).Where(eq("task_id", x.ID)).Update("consumed", true).Error; e != nil {
		t.Fatal(e)
	}
	msgs := []modelclient.Message{{Role: "system", Content: "test"}, {Role: "assistant", ToolCalls: []modelclient.ToolCall{{ID: "call"}}}}
	if e = db.Model(&Task{}).Where(eq("id", x.ID)).Updates(&Task{Messages: msgs, ContextReady: true}).Error; e != nil {
		t.Fatal(e)
	}
	c := ToolCall{ID: xid.New("call"), TaskID: x.ID, ModelID: "call", Status: "completed", Result: "tool result"}
	if e = db.Create(&c).Error; e != nil {
		t.Fatal(e)
	}
	if e = Steer(ctx, db, p, x.ID, "steering"); e != nil {
		t.Fatal(e)
	}
	w := Worker{DB: db, Model: fakeModel{}}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	var current Task
	db.Where(eq("id", x.ID)).Take(&current)
	if len(current.Messages) != 3 || current.Messages[2].Role != "tool" {
		t.Fatal("steering split the tool call/result pair", current.Messages)
	}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	db.Where(eq("id", x.ID)).Take(&current)
	if current.Messages[3].Role != "user" {
		t.Fatal("steering not consumed after tool completion")
	}
}

func TestModelDrivenDelegation(t *testing.T) {
	db, p, s := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expert := agent(t, db, p, "expert", nil)
	a, e := agents.Create(ctx, db, p, agents.Config{Name: "root", Model: "any", Instructions: "coordinator", ExpertIDs: []string{expert.ID}})
	if e != nil {
		t.Fatal(e)
	}
	root := task(t, db, p, s, a)
	model := modelFunc(func(ctx context.Context, r modelclient.Request, d func(modelclient.Delta)) (*modelclient.Final, error) {
		instruction, _ := r.Messages[0].Content.(string)
		if !strings.HasPrefix(instruction, "coordinator") {
			return fakeModel{}.Stream(ctx, r, d)
		}
		calls := 0
		for _, m := range r.Messages {
			if m.Role == "tool" {
				calls++
			}
		}
		name, args := "agent_spawn", `{"agent_id":"`+expert.ID+`","text":"solve the subtask"}`
		if calls == 1 {
			name, args = "agent_wait", `{}`
		}
		if calls >= 2 {
			return fakeModel{}.Stream(ctx, r, d)
		}
		return &modelclient.Final{ToolCalls: []modelclient.ToolCall{{ID: name, Type: "function", Function: modelclient.FuncCallSpec{Name: name, Arguments: args}}}, StopReason: "tool_calls"}, nil
	})
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { (&Worker{DB: db, Model: model, Lease: 5 * time.Second}).Run(ctx) })
	}
	defer func() { cancel(); wg.Wait() }()
	waitState(t, db, root.ID, "succeeded")
	var children []Task
	db.Where(eq("parent_id", root.ID)).Find(&children)
	if len(children) != 1 || children[0].State != "succeeded" {
		t.Fatal(children)
	}
}

func TestCancelActiveModel(t *testing.T) {
	db, p, s := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := agent(t, db, p, "block", nil)
	root := task(t, db, p, s, a)
	started := make(chan struct{}, 1)
	model := modelFunc(func(ctx context.Context, r modelclient.Request, d func(modelclient.Delta)) (*modelclient.Final, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	done := make(chan struct{})
	go func() { defer close(done); (&Worker{DB: db, Model: model, Lease: 5 * time.Second}).Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model not started")
	}
	if e := Cancel(ctx, db, p, root.ID); e != nil {
		t.Fatal(e)
	}
	waitState(t, db, root.ID, "canceled")
}

func TestCompactedHistorySurvivesNextRoot(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "current instructions", nil)
	previous := task(t, db, p, s, a)
	finished := time.Now()
	messages := []modelclient.Message{{Role: "system", Content: "previous instructions"}, {Role: "user", Content: "Conversation summary: retained fact"}, {Role: "assistant", Content: "recent answer"}}
	if e := db.Model(&Task{}).Where(eq("id", previous.ID)).Updates(&Task{Messages: messages, State: "succeeded", FinishedAt: &finished}).Error; e != nil {
		t.Fatal(e)
	}
	next := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "test", time.Minute)
	if e != nil || claim == nil || claim.ID != next.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	if e = (&Worker{DB: db, Model: fakeModel{}}).step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	var current Task
	if e = db.Where(eq("id", next.ID)).Take(&current).Error; e != nil {
		t.Fatal(e)
	}
	if len(current.Messages) < 4 || current.Messages[1].Content != "Conversation summary: retained fact" || current.Messages[0].Content != "current instructions" {
		t.Fatal("summary lost or agent instructions inherited incorrectly", current.Messages)
	}
}

func TestInterruptedFinalizationRequiresReconciliation(t *testing.T) {
	db, p, s := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "test", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	started := make(chan struct{})
	done := make(chan struct{})
	w := Worker{DB: db, Model: fakeModel{}, Lease: time.Minute, Finish: func(ctx context.Context, s Session, t Task) error { close(started); <-ctx.Done(); return ctx.Err() }}
	go func() { defer close(done); w.run(ctx, claim) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("finalization not started")
	}
	cancel()
	<-done
	waitState(t, db, x.ID, "unknown")
	if next, e := Claim(context.Background(), db, "other", time.Minute); e != nil || next != nil {
		t.Fatalf("uncertain finalization automatically replayed: %v %v", next, e)
	}
}
