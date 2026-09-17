package execution

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/model"
	"wave-ai.local/wave/internal/platform/xid"
)

// Regression tests for targeted-update regressions: every hot-path write must
// persist every column its step actually mutates, and renewal must respect
// lease validity. Each test fails against the regressed code it guards.

func TestRegressionChildToolCallsAccumulateToRoot(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	tools := []agents.Tool{{Name: "write", Kind: "builtin"}}
	root := agent(t, db, p, "", tools)
	expert := agent(t, db, p, "write", tools)
	cfg := root.Config
	cfg.ExpertIDs = []string{expert.ID}
	var e error
	root, e = agents.Update(ctx, db, p, root.ID, root.Version, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tr := task(t, db, p, s, root)
	first, e := Spawn(ctx, db, p, tr.ID, expert.ID, "one")
	if e != nil {
		t.Fatal(e)
	}
	second, e := Spawn(ctx, db, p, tr.ID, expert.ID, "two")
	if e != nil {
		t.Fatal(e)
	}
	workers(t, db, 2, func(context.Context, Session, ToolCall) (ToolResult, error) {
		return ToolResult{Output: "written"}, nil
	})
	waitState(t, db, first.ID, "succeeded")
	waitState(t, db, second.ID, "succeeded")
	var row Task
	if e = db.Where(eq("id", tr.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.ToolCount != 2 {
		t.Fatalf("root tool_count=%d want 2; children bypassed the shared tool budget", row.ToolCount)
	}
}

func TestRegressionRootBudgetRecheckedAfterCompaction(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	tools := []agents.Tool{{Name: "write", Kind: "builtin"}}
	expert := agent(t, db, p, "write", tools)
	root := agent(t, db, p, "", tools)
	cfg := root.Config
	cfg.ExpertIDs = []string{expert.ID}
	var e error
	root, e = agents.Update(ctx, db, p, root.ID, root.Version, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tr := task(t, db, p, s, root)
	// Keep the root leased by a foreign owner so workers only run the child.
	if _, e := Claim(ctx, db, "holder", time.Minute); e != nil {
		t.Fatal(e)
	}
	tr.Budget.MaxTokens = 100
	if e := db.Model(&Task{}).Where(eq("id", tr.ID)).Update("budget", tr.Budget); e.Error != nil {
		t.Fatal(e.Error)
	}
	// Pre-charge the root as sibling spending would: 90 of 100 tokens used.
	// Compaction must then push the root past its ceiling, while the child
	// itself still has headroom.
	if e := db.Model(&Task{}).Where(eq("id", tr.ID)).Update("used_tokens", 90); e.Error != nil {
		t.Fatal(e.Error)
	}
	child, e := Spawn(ctx, db, p, tr.ID, expert.ID, "write", Budget{MaxTokens: 100, MaxToolCalls: 10, MaxSeconds: 600})
	if e != nil {
		t.Fatal(e)
	}
	// Long second input plus short fillers: compaction keeps messages[0], a
	// summary, and the tail; the long content sits inside the compacted range.
	long := strings.Repeat("y", 1000)
	e = db.Transaction(func(tx *gorm.DB) error {
		var sess Session
		if e := tx.Where(eq("id", s.ID)).Take(&sess).Error; e != nil {
			return e
		}
		if e := addInput(tx, &sess, &child, long, "user", ""); e != nil {
			return e
		}
		for range 11 {
			if e := addInput(tx, &sess, &child, "x", "user", ""); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	wctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		(&Worker{DB: db, Model: fakeModel{}, Lease: 5 * time.Second, ContextTokens: 1024, Execute: func(context.Context, Session, ToolCall) (ToolResult, error) {
			return ToolResult{Output: "written"}, nil
		}}).Run(wctx)
	})
	t.Cleanup(func() { cancel(); wg.Wait() })
	out := waitState(t, db, child.ID, "failed")
	if !strings.Contains(out.Error, "after context compaction") {
		t.Fatalf("child error=%q want root budget enforced after compaction", out.Error)
	}
	compacted := false
	events, e := Events(ctx, db, s.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, ev := range events {
		if ev.Type == "context.compacted" && ev.TaskID == child.ID {
			compacted = true
		}
	}
	if !compacted {
		t.Fatal("context.compacted event missing; compaction never ran")
	}
}

func TestRegressionRenewalCannotResurrectExpiredLease(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	one, e := Claim(ctx, db, "old", time.Minute)
	if e != nil || one == nil || one.ID != x.ID {
		t.Fatalf("claim: %v %v", one, e)
	}
	w := &Worker{DB: db, Lease: 5 * time.Second}
	if !w.renewLease(ctx, one, func() {}) {
		t.Fatal("renewal of a live lease rejected")
	}
	var row Task
	if e = db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.LeaseUntil == nil || !row.LeaseUntil.After(time.Now()) {
		t.Fatal("live lease not extended")
	}
	if e = db.Model(&Task{}).Where(eq("id", x.ID)).Update("lease_until", time.Now().Add(-time.Second)).Error; e != nil {
		t.Fatal(e)
	}
	if w.renewLease(ctx, one, func() {}) {
		t.Fatal("expired lease resurrected by renewal")
	}
	if e = db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.LeaseUntil != nil && row.LeaseUntil.After(time.Now()) {
		t.Fatal("expired lease was extended in the database")
	}
	two, e := Claim(ctx, db, "new", time.Minute)
	if e != nil || two == nil {
		t.Fatal(e)
	}
	if w.renewLease(ctx, one, func() {}) {
		t.Fatal("renewal accepted a task taken over by another worker")
	}
}

func TestRegressionAdmissionFailurePersisted(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	admit := func(_ *gorm.DB, _ Session, t *Task) (bool, error) {
		// Mirrors admitSandbox on ErrResources: claim proceeds, task fails.
		t.PendingFinish, t.Error = "failed", "insufficient sandbox resources"
		return true, nil
	}
	if _, e := claimWithAdmission(ctx, db, xid.New("worker"), time.Minute, admit); e != nil {
		t.Fatal(e)
	}
	var row Task
	if e := db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.State != "running" {
		t.Fatalf("state=%s want running", row.State)
	}
	if row.PendingFinish != "failed" || row.Error == "" {
		t.Fatalf("admission failure lost: pending_finish=%q error=%q", row.PendingFinish, row.Error)
	}
}

func TestRegressionParkRequeuesProgressedCalls(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	for _, tc := range []struct {
		status    string
		delivered bool
		want      string
	}{
		// A call still awaiting the client is a legitimate waiting state.
		{"custom", false, "waiting"},
		// Resolve committing between the worker snapshot and park must not
		// strand the call: the task requeues instead of parking.
		{"ready", false, "queued"},
		{"completed", false, "queued"},
		{"completed", true, "waiting"},
	} {
		tr := task(t, db, p, s, a)
		// Simulate a claimed task so the worker fence accepts the park.
		claim := tr
		claim.Owner, claim.Epoch = "test", 1
		lease := time.Now().Add(time.Minute)
		if e := db.Model(&Task{}).Where(eq("id", tr.ID)).Updates(map[string]any{"owner": "test", "epoch": 1, "state": "running", "lease_until": lease}).Error; e != nil {
			t.Fatal(e)
		}
		call := ToolCall{ID: xid.New("call"), TaskID: tr.ID, ModelID: "m1", Tool: agents.Tool{Name: "t", Kind: "custom"}, Arguments: "{}", Status: tc.status, Delivered: tc.delivered, Result: "r"}
		if e := db.Create(&call).Error; e != nil {
			t.Fatal(e)
		}
		w := &Worker{DB: db}
		if e := w.park(context.Background(), &claim, "waiting"); e != nil {
			t.Fatal(e)
		}
		var row Task
		if e := db.Where(eq("id", tr.ID)).Take(&row).Error; e != nil {
			t.Fatal(e)
		}
		if row.State != tc.want {
			t.Fatalf("call status=%s delivered=%v: task state=%s want=%s", tc.status, tc.delivered, row.State, tc.want)
		}
	}
}

// A tight token budget must cap the per-request output tokens sent to the
// provider; without the cap a single generation can overshoot any budget
// (DeepSeek ignores cross-step-only enforcement and max_completion_tokens).
func TestRegressionTightBudgetCapsOutputTokens(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	tr := task(t, db, p, s, a)
	tr.Budget.MaxTokens = 50
	if e := db.Model(&Task{}).Where(eq("id", tr.ID)).Updates(map[string]any{"budget": tr.Budget, "used_tokens": int64(10)}).Error; e != nil {
		t.Fatal(e)
	}
	var got []int
	capturing := captureModel{fn: func(r model.Request) {
		got = append(got, r.MaxOutputTokens)
	}}
	w := &Worker{DB: db, Model: capturing, Lease: 5 * time.Second}
	claim, e := Claim(context.Background(), db, "tester", 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if claim == nil || claim.ID != tr.ID {
		t.Fatal("claim missed task")
	}
	if e := w.step(context.Background(), claim); e != nil {
		t.Fatal(e)
	}
	if len(got) == 0 {
		t.Fatal("no generation issued")
	}
	if got[0] != 40 {
		t.Fatalf("output cap = %d, want remaining budget 40", got[0])
	}
}

// Comfortably large budgets stay uncapped: providers reject limits above their
// own output ceilings, so the cap only applies when the budget binds.
func TestRegressionLargeBudgetStaysUncapped(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	tr := task(t, db, p, s, a)
	var got []int
	capturing := captureModel{fn: func(r model.Request) {
		got = append(got, r.MaxOutputTokens)
	}}
	w := &Worker{DB: db, Model: capturing, Lease: 5 * time.Second}
	claim, e := Claim(context.Background(), db, "tester", 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if claim == nil || claim.ID != tr.ID {
		t.Fatal("claim missed task")
	}
	if e := w.step(context.Background(), claim); e != nil {
		t.Fatal(e)
	}
	if len(got) == 0 {
		t.Fatal("no generation issued")
	}
	if got[0] != 0 {
		t.Fatalf("default-budget cap = %d, want 0 (unset)", got[0])
	}
}

type captureModel struct {
	fn func(model.Request)
}

func (c captureModel) Stream(ctx context.Context, r model.Request, delta func(model.Delta)) (*model.Final, error) {
	c.fn(r)
	n := int64(3)
	return &model.Final{Content: []model.ContentBlock{{Type: "text", Text: "answer"}}, Usage: model.Usage{PromptTokens: &n, CompletionTokens: &n}, StopReason: "stop"}, nil
}
