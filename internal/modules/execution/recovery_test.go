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
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestDurableActionsResultsAndHistory(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "custom", []agents.Tool{{Name: "custom", Kind: "custom", Approval: true, Parameters: map[string]any{"type": "object"}}})
	root := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "first", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	w := Worker{DB: db, Model: fakeModel{}}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	actions, e := RequiredActions(ctx, db, p, s.ID)
	if e != nil || len(actions) != 1 || actions[0].Type != "approve_tool" {
		t.Fatalf("actions=%+v %v", actions, e)
	}
	callID := actions[0].CallID
	yes, no := true, false
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if e := Resolve(ctx, db, p, root.ID, callID, ToolResult{}, &yes); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if e = Resolve(ctx, db, p, root.ID, callID, ToolResult{}, &no); e == nil {
		t.Fatal("changed approval accepted")
	}
	actions, e = RequiredActions(ctx, db.Session(&gorm.Session{NewDB: true}), p, s.ID)
	if e != nil || len(actions) != 1 || actions[0].Type != "submit_tool_result" {
		t.Fatalf("reconnected actions=%+v %v", actions, e)
	}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	result := ToolFailed("remote_failure", "confirmed failure")
	for range 2 {
		if e = Resolve(ctx, db, p, root.ID, callID, result, nil); e != nil {
			t.Fatal(e)
		}
	}
	if e = Resolve(ctx, db, p, root.ID, callID, ToolResult{Output: result.Output}, nil); e == nil {
		t.Fatal("changed outcome accepted")
	}
	claim, e = Claim(ctx, db, "reconnected", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	if e = Resolve(ctx, db, p, root.ID, callID, result, nil); e != nil {
		t.Fatal("duplicate after finish", e)
	}
	if e = Resolve(ctx, db, p, root.ID, callID, ToolResult{}, &yes); e != nil {
		t.Fatal("approval retry after finish", e)
	}
	history, e := History(ctx, db, p, s.ID, root.ID, 0)
	if e != nil || len(history) != 4 {
		t.Fatalf("history %+v %v", history, e)
	}
	if history[2].Role != "tool" || !history[2].IsError || history[2].CallID != callID {
		t.Fatal("tool outcome lost", history[2])
	}
	for i, m := range history {
		if m.ID == "" || m.Seq != int64(i+1) {
			t.Fatal("unstable message identity", history)
		}
	}
	tail, e := History(ctx, db, p, s.ID, root.ID, history[2].Seq)
	if e != nil || len(tail) != 1 || tail[0].ID != history[3].ID {
		t.Fatal("history cursor", tail, e)
	}
	rows, e := Events(ctx, db, s.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, ev := range rows {
		if ev.Type == "message.delta" {
			found = true
			if ev.Data["message_id"] != history[3].ID {
				t.Fatal("preview identity mismatch")
			}
		}
	}
	if !found {
		t.Fatal("missing preview")
	}
	actions, e = RequiredActions(ctx, db, p, s.ID)
	if e != nil || len(actions) != 0 {
		t.Fatal("stale required action", actions, e)
	}
	outsider := &auth.Principal{OrgID: p.OrgID, PrincipalID: "00000000-0000-4000-8000-000000000001"}
	if _, e = History(ctx, db, outsider, s.ID, "", 0); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("history isolation", e)
	}
	if _, e = RequiredActions(ctx, db, outsider, s.ID); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("action isolation", e)
	}
	var generations []Generation
	if e = db.Where(eq("task_id", root.ID)).Find(&generations).Error; e != nil {
		t.Fatal(e)
	}
	if len(generations) != 2 || generations[1].DurationMS == nil || generations[1].Usage.PromptTokens == nil {
		t.Fatal("missing call metrics", generations)
	}
}

func TestConfirmedFailureVersusUnknownSideEffect(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{true: "unknown", false: "confirmed"}[uncertain], func(t *testing.T) {
			db, p, s := setup(t)
			ctx := context.Background()
			a := agent(t, db, p, "", nil)
			root := task(t, db, p, s, a)
			claim, e := Claim(ctx, db, "worker", time.Minute)
			if e != nil || claim == nil {
				t.Fatal(e)
			}
			call := ToolCall{ID: xid.New("call"), TaskID: root.ID, ModelID: "write", Tool: agents.Tool{Kind: "builtin", Name: "write"}, Status: "ready"}
			if e = db.Create(&call).Error; e != nil {
				t.Fatal(e)
			}
			executions := 0
			w := Worker{DB: db, Execute: func(context.Context, Session, ToolCall) (ToolResult, error) {
				executions++
				if uncertain {
					return ToolResult{}, errors.New("connection lost after dispatch")
				}
				return ToolFailed("command_failed", "exit 1"), nil
			}}
			if e = w.tool(ctx, claim, s, call); e != nil {
				t.Fatal(e)
			}
			if e = db.Where(eq("id", call.ID)).Take(&call).Error; e != nil {
				t.Fatal(e)
			}
			if e = db.Where(eq("id", s.ID)).Take(&s).Error; e != nil {
				t.Fatal(e)
			}
			if call.StartedAt == nil {
				t.Fatal("tool start missing")
			}
			actions, e := RequiredActions(ctx, db, p, s.ID)
			if e != nil {
				t.Fatal(e)
			}
			if uncertain {
				if call.Status != "unknown" || s.WriterCall != call.ID || len(actions) != 1 || actions[0].Type != "confirm_tool_outcome" {
					t.Fatal(call, actions)
				}
				if next, e := Claim(ctx, db, "new", time.Minute); e != nil || next != nil {
					t.Fatal("uncertain call replayed", next, e)
				}
				if e = Resolve(ctx, db, p, root.ID, call.ID, ToolResult{Output: "confirmed written"}, nil); e != nil {
					t.Fatal(e)
				}
			} else if call.Status != "completed" || !call.IsError || call.FinishedAt == nil || s.WriterCall != "" || len(actions) != 0 {
				t.Fatal("failure parked as unknown", call, actions)
			}
			if executions != 1 {
				t.Fatal("replayed tool")
			}
		})
	}
}

func TestCompactionPreservesHistoryAndPriority(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "Keep explicit constraints", nil)
	root := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "worker", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	e = withTask(ctx, db, root.ID, func(tx *gorm.DB, s *Session, current *Task) error {
		for i := range 40 {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			text := strings.Repeat("context ", 44)
			current.Messages = append(current.Messages, modelclient.Message{Role: role, Content: text})
			if e := appendMessage(tx, s, current, Message{Role: role, Text: text, Source: "user"}); e != nil {
				return e
			}
		}
		root = *current
		return tx.Save(current).Error
	})
	if e != nil {
		t.Fatal(e)
	}
	before, e := History(ctx, db, p, s.ID, "", 0)
	if e != nil {
		t.Fatal(e)
	}
	w := Worker{DB: db, ContextTokens: 8000, Model: modelFunc(func(ctx context.Context, r modelclient.Request, d func(modelclient.Delta)) (*modelclient.Final, error) {
		if r.MaxOutputTokens != 2000 || len(r.Tools) != 0 {
			t.Fatal("unbounded summary request", r.MaxOutputTokens)
		}
		n := int64(10)
		return &modelclient.Final{Content: []modelclient.ContentBlock{{Type: "text", Text: "Objective and explicit constraints retained; next complete tests."}}, Usage: modelclient.Usage{PromptTokens: &n, CompletionTokens: &n}}, nil
	})}
	out, e := w.compact(ctx, claim, root)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Messages) >= len(root.Messages) || out.Messages[0].Content != "Keep explicit constraints" || out.Messages[1].Role != "user" {
		t.Fatal("compaction changed instruction priority", out.Messages)
	}
	if out.UsedTokens != 20 {
		t.Fatal("summary usage omitted", out.UsedTokens)
	}
	after, e := History(ctx, db, p, s.ID, "", 0)
	if e != nil || len(after) != len(before) {
		t.Fatal("compaction mutated history", e)
	}
	for i := range before {
		if before[i].ID != after[i].ID || before[i].Text != after[i].Text {
			t.Fatal("history changed")
		}
	}
}

func TestSubagentConcurrencyIsNotLifetimeBudget(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	expert := agent(t, db, p, "expert", nil)
	a, e := agents.Create(ctx, db, p, agents.Config{Name: "root", Model: "any", ExpertIDs: []string{expert.ID}})
	if e != nil {
		t.Fatal(e)
	}
	root, e := CreateTask(ctx, db, p, s.ID, a.ID, "work", Budget{MaxAgents: 4, MaxConcurrentAgents: 1})
	if e != nil {
		t.Fatal(e)
	}
	first, e := Spawn(ctx, db, p, root.ID, expert.ID, "first")
	if e != nil {
		t.Fatal(e)
	}
	second, e := Spawn(ctx, db, p, root.ID, expert.ID, "second")
	if e != nil {
		t.Fatal(e)
	}
	w := Worker{DB: db}
	claim, e := Claim(ctx, db, "coordinator", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	if e = w.park(ctx, claim, "waiting"); e != nil {
		t.Fatal(e)
	}
	child, e := Claim(ctx, db, "one", time.Minute)
	if e != nil || child == nil || child.ID != first.ID {
		t.Fatal("first", child, e)
	}
	if blocked, e := Claim(ctx, db, "two", time.Minute); e != nil || blocked != nil {
		t.Fatal("concurrency cap ignored", blocked, e)
	}
	if e = w.finish(ctx, child, "succeeded"); e != nil {
		t.Fatal(e)
	}
	claim, e = Claim(ctx, db, "coordinator", time.Minute)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	if e = w.park(ctx, claim, "waiting"); e != nil {
		t.Fatal(e)
	}
	child, e = Claim(ctx, db, "two", time.Minute)
	if e != nil || child == nil || child.ID != second.ID {
		t.Fatal("slot not released", child, e)
	}
	if _, e = Spawn(ctx, db, p, root.ID, expert.ID, "third"); e != nil {
		t.Fatal("lifetime budget consumed by concurrency", e)
	}
	if _, e = Spawn(ctx, db, p, root.ID, expert.ID, "fourth"); e == nil {
		t.Fatal("lifetime cap reset after child finish")
	}
}
