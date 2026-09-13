package execution

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

type fakeModel struct{}

func (fakeModel) Stream(ctx context.Context, r modelclient.Request, delta func(modelclient.Delta)) (*modelclient.Final, error) {
	var instruction string
	if len(r.Messages) > 0 {
		instruction, _ = r.Messages[0].Content.(string)
		instruction = strings.Split(instruction, "\n")[0]
	}
	if instruction == "block" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if instruction == "write" || instruction == "custom" {
		has := false
		for _, m := range r.Messages {
			if m.Role == "tool" {
				has = true
			}
		}
		if !has {
			return &modelclient.Final{ToolCalls: []modelclient.ToolCall{{ID: "model-call", Type: "function", Function: modelclient.FuncCallSpec{Name: instruction, Arguments: `{"path":"/workspace/a","content":"hello"}`}}}, StopReason: "tool_calls"}, nil
		}
	}
	delta(modelclient.Delta{Text: "answer"})
	n := int64(3)
	return &modelclient.Final{Content: []modelclient.ContentBlock{{Type: "text", Text: "answer"}}, Usage: modelclient.Usage{PromptTokens: &n, CompletionTokens: &n}, StopReason: "stop"}, nil
}
func setup(t *testing.T) (*gorm.DB, *auth.Principal, Session) {
	t.Helper()
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, e := database.Open(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	models := append(auth.Models(), &agents.Agent{}, &agents.Version{})
	models = append(models, Models()...)
	if e = db.AutoMigrate(models...); e != nil {
		t.Fatal(e)
	}
	key, e := auth.Bootstrap(context.Background(), db, xid.New("org"), "admin", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	p, e := auth.Authenticate(context.Background(), db, key)
	if e != nil {
		t.Fatal(e)
	}
	s := Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID}
	if e = db.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Model(&Task{}).Where(eq("session_id", s.ID)).Updates(map[string]any{"state": "canceled", "owner": "", "lease_until": nil})
		db.Model(&Session{}).Where(eq("id", s.ID)).Update("active_root", "")
	})
	return db, p, s
}
func agent(t *testing.T, db *gorm.DB, p *auth.Principal, instruction string, tools []agents.Tool) agents.Agent {
	t.Helper()
	a, e := agents.Create(context.Background(), db, p, agents.Config{Name: "test", Model: "any-model", Instructions: instruction, Tools: tools})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func task(t *testing.T, db *gorm.DB, p *auth.Principal, s Session, a agents.Agent) Task {
	t.Helper()
	x, e := CreateTask(context.Background(), db, p, s.ID, a.ID, "hello", Budget{})
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func waitState(t *testing.T, db *gorm.DB, id, state string) Task {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	var row Task
	for time.Now().Before(until) {
		if e := db.Where(eq("id", id)).Take(&row).Error; e != nil {
			t.Fatal(e)
		}
		if row.State == state {
			return row
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s state=%s want=%s err=%s", id, row.State, state, row.Error)
	return row
}
func workers(t *testing.T, db *gorm.DB, n int, execute func(context.Context, Session, ToolCall) (ToolResult, error)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { (&Worker{DB: db, Model: fakeModel{}, Lease: 5 * time.Second, Execute: execute}).Run(ctx) })
	}
	t.Cleanup(func() { cancel(); wg.Wait() })
}
func TestPostgresExecution(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	t.Run("fencing", func(t *testing.T) {
		a := agent(t, db, p, "", nil)
		x := task(t, db, p, s, a)
		one, e := Claim(ctx, db, "old", time.Second)
		if e != nil || one == nil || one.ID != x.ID {
			t.Fatalf("claim: %v %v", one, e)
		}
		if e = db.Model(&Task{}).Where(eq("id", x.ID)).Update("lease_until", time.Now().Add(-time.Second)).Error; e != nil {
			t.Fatal(e)
		}
		two, e := Claim(ctx, db, "new", time.Second)
		if e != nil || two == nil {
			t.Fatal(e)
		}
		if e = fenced(ctx, db, one, func(*gorm.DB, *Session, *Task) error { return nil }); !errors.Is(e, ErrFence) {
			t.Fatalf("stale owner accepted: %v", e)
		}
		db.Model(&Task{}).Where(eq("id", x.ID)).Update("state", "canceled")
		db.Model(&Session{}).Where(eq("id", s.ID)).Update("active_root", "")
	})
	t.Run("model and events", func(t *testing.T) {
		a := agent(t, db, p, "", nil)
		x := task(t, db, p, s, a)
		workers(t, db, 2, nil)
		out := waitState(t, db, x.ID, "succeeded")
		if out.UsedTokens != 6 || !out.UsageKnown {
			t.Fatalf("usage %+v", out)
		}
		rows, e := Events(ctx, db, s.ID, 0)
		if e != nil || len(rows) < 4 {
			t.Fatalf("events %v %v", rows, e)
		}
		for i := 1; i < len(rows); i++ {
			if rows[i].Seq != rows[i-1].Seq+1 {
				t.Fatal("event gap")
			}
		}
	})
	t.Run("custom result", func(t *testing.T) {
		a := agent(t, db, p, "custom", []agents.Tool{{Name: "custom", Kind: "custom", Parameters: map[string]any{"type": "object"}}})
		x := task(t, db, p, s, a)
		workers(t, db, 2, nil)
		waitState(t, db, x.ID, "waiting")
		var call ToolCall
		if e := db.Where(eq("task_id", x.ID)).Take(&call).Error; e != nil {
			t.Fatal(e)
		}
		if e := Resolve(ctx, db, p, x.ID, call.ID, ToolResult{Output: "done"}, nil); e != nil {
			t.Fatal(e)
		}
		waitState(t, db, x.ID, "succeeded")
	})
	t.Run("approval and serialized child writes", func(t *testing.T) {
		tools := []agents.Tool{{Name: "write", Kind: "builtin", Approval: true}}
		a := agent(t, db, p, "", tools)
		expert := agent(t, db, p, "write", tools)

		cfg := a.Config
		cfg.ExpertIDs = []string{expert.ID}
		var updateErr error
		a, updateErr = agents.Update(ctx, db, p, a.ID, a.Version, cfg)
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		root := task(t, db, p, s, a)
		first, e := Spawn(ctx, db, p, root.ID, expert.ID, "one")
		if e != nil {
			t.Fatal(e)
		}
		second, e := Spawn(ctx, db, p, root.ID, expert.ID, "two")
		if e != nil {
			t.Fatal(e)
		}
		var active, max atomic.Int32
		workers(t, db, 4, func(context.Context, Session, ToolCall) (ToolResult, error) {
			n := active.Add(1)
			for {
				old := max.Load()
				if old >= n || max.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(80 * time.Millisecond)
			active.Add(-1)
			return ToolResult{Output: "written"}, nil
		})
		for _, id := range []string{first.ID, second.ID} {
			waitState(t, db, id, "waiting")
			var call ToolCall
			if e = db.Where(eq("task_id", id)).Take(&call).Error; e != nil {
				t.Fatal(e)
			}
			yes := true
			if e = Resolve(ctx, db, p, id, call.ID, ToolResult{Output: ""}, &yes); e != nil {
				t.Fatal(e)
			}
		}
		waitState(t, db, root.ID, "succeeded")
		if max.Load() != 1 {
			t.Fatalf("concurrent writers=%d", max.Load())
		}
	})
	t.Run("cancel versus spawn", func(t *testing.T) {

		a := agent(t, db, p, "block", nil)
		cfg := a.Config
		cfg.ExpertIDs = []string{a.ID}
		var e error
		a, e = agents.Update(ctx, db, p, a.ID, a.Version, cfg)
		if e != nil {
			t.Fatal(e)
		}
		root := task(t, db, p, s, a)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() { _, _ = Spawn(ctx, db, p, root.ID, a.ID, "child") })
		}
		wg.Go(func() {
			if e := Cancel(ctx, db, p, root.ID); e != nil {
				t.Error(e)
			}
		})
		wg.Wait()
		if _, e := Spawn(ctx, db, p, root.ID, a.ID, "late"); e == nil {
			t.Fatal("spawn after cancel accepted")
		}
		rows := []Task{}
		db.Where(eq("root_id", root.ID)).Find(&rows)
		for _, r := range rows {
			if !r.CancelRequested {
				t.Fatal("child missed cancellation")
			}
		}
		workers(t, db, 3, nil)
		waitState(t, db, root.ID, "canceled")
	})
	t.Run("unknown tool is never replayed", func(t *testing.T) {
		a := agent(t, db, p, "", nil)
		x := task(t, db, p, s, a)
		claim, e := Claim(ctx, db, "gone", time.Second)
		if e != nil || claim == nil {
			t.Fatal(e)
		}
		call := ToolCall{ID: xid.New("call"), TaskID: x.ID, ModelID: "call", Status: "running"}
		if e = db.Create(&call).Error; e != nil {
			t.Fatal(e)
		}
		db.Model(&Task{}).Where(eq("id", x.ID)).Update("lease_until", time.Now().Add(-time.Second))
		again, e := Claim(ctx, db, "recovery", time.Second)
		if e != nil || again != nil {
			t.Fatalf("unexpected claim %+v %v", again, e)
		}
		waitState(t, db, x.ID, "unknown")
		var persisted ToolCall
		db.Where(eq("id", call.ID)).Take(&persisted)
		if persisted.Status != "unknown" {
			t.Fatal("tool outcome not marked unknown")
		}
		if e = Resolve(ctx, db, p, x.ID, call.ID, ToolResult{Output: "confirmed result"}, nil); e != nil {
			t.Fatal(e)
		}
		workers(t, db, 1, nil)
		waitState(t, db, x.ID, "succeeded")
	})
	t.Run("input sequence concurrency", func(t *testing.T) {
		a := agent(t, db, p, "", nil)
		x := task(t, db, p, s, a)
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				if e := Steer(ctx, db, p, x.ID, "steering"); e != nil {
					t.Error(e)
				}
			})
		}
		wg.Wait()
		var count int64
		db.Model(&Input{}).Where(clause.Eq{Column: "task_id", Value: x.ID}).Count(&count)
		if count != 21 {
			t.Fatalf("inputs=%d", count)
		}
	})
}
