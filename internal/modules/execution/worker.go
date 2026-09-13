package execution

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/guardrails"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

type Model interface {
	Stream(context.Context, modelclient.Request, func(modelclient.Delta)) (*modelclient.Final, error)
}
type Worker struct {
	Guard         *guardrails.Set
	ContextTokens int
	DB            *gorm.DB
	Model         Model
	Lease         time.Duration
	Execute       func(context.Context, Session, ToolCall) (ToolResult, error)
	Prepare       func(context.Context, Session, Task) error
	Finish        func(context.Context, Session, Task) error
}

func (w *Worker) Run(ctx context.Context) {
	if w.Lease == 0 {
		w.Lease = 30 * time.Second
	}
	owner := xid.New("worker")
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		t, e := Claim(ctx, w.DB, owner, w.Lease)
		if e != nil {
			slog.Error("claim", "error", e)
			continue
		}
		if t == nil {
			continue
		}
		w.run(ctx, t)
	}
}
func (w *Worker) run(ctx context.Context, t *Task) {
	run, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer cancel()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
			}
			e := fenced(run, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
				until := time.Now().Add(w.Lease)
				current.LeaseUntil = &until
				if e := tx.Model(current).Update("lease_until", until).Error; e != nil {
					return e
				}
				if current.CancelRequested && !t.CancelRequested {
					cancel()
				}
				return nil
			})
			if e != nil {
				return
			}
		}
	})
	defer func() {
		cancel()
		wg.Wait()
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_ = fenced(cleanup, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
			if e := interruptGenerations(tx, current); e != nil {
				return e
			}
			var running int64
			if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Count(&running).Error; e != nil {
				return e
			}
			current.State = "queued"
			if current.Finalizing {
				current.State = "unknown"
				current.Error = "finalization interrupted; confirm external operations stopped before reconciliation"
			}
			if running > 0 {
				current.State = "unknown"
				current.Error = "worker interrupted during tool execution"
				if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Update("status", "unknown").Error; e != nil {
					return e
				}
			}
			current.Owner = ""
			current.LeaseUntil = nil
			if e := tx.Save(current).Error; e != nil {
				return e
			}
			if current.State == "unknown" {
				return emit(tx, s, t.ID, "task.unknown", map[string]any{"reason": current.Error})
			}
			return emit(tx, s, t.ID, "generation.interrupted", map[string]any{"attempt": current.Attempts})
		})
		if rec := recover(); rec != nil {
			slog.Error("worker panic", "task", t.ID, "error", rec)
		}
	}()
	for run.Err() == nil {
		if e := w.step(run, t); e != nil {
			if !errors.Is(e, ErrFence) {
				slog.Warn("execution step", "task", t.ID, "error", e)
			}
			return
		}
		var current Task
		if e := w.DB.WithContext(run).Select("state").Where(eq("id", t.ID)).Take(&current).Error; e != nil || current.State != "running" {
			return
		}
	}

}
func principal(s Session) *auth.Principal {
	return &auth.Principal{OrgID: s.OrgID, PrincipalID: s.OwnerID, Scope: auth.ScopeAPI}
}
