package execution

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/guardrails"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/model"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

type Model interface {
	Stream(context.Context, model.Request, func(model.Delta)) (*model.Final, error)
}
type Worker struct {
	WriteLog      func(observe.Log)
	Guard         *guardrails.Set
	ContextTokens int
	DB            *gorm.DB
	Model         Model
	Lease         time.Duration
	Execute       func(context.Context, Session, ToolCall) (ToolResult, error)
	Prepare       func(context.Context, Session, Task) error
	Evaluate      func(context.Context, Session, Task) *Evaluation
	Finish        func(context.Context, Session, Task) error
	Admit         Admission
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
		t, e := claimWithAdmission(ctx, w.DB, owner, w.Lease, w.Admit)
		if e != nil {
			slog.Error("task.claim_failed", "error_kind", observe.ErrorKind(e))
			continue
		}
		if t == nil {
			continue
		}
		w.run(ctx, t)
	}
}
func (w *Worker) run(ctx context.Context, t *Task) {
	ctx = w.traceContext(ctx, t)
	observe.Logger(ctx).Info("task.claimed", "epoch", t.Epoch, "worker_id", t.Owner)
	defer func() { observe.Logger(ctx).Info("task.released", "epoch", t.Epoch) }()
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
			if !w.renewLease(run, t, cancel) {
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
			observe.Logger(ctx).Error("worker.panic")
		}
	}()
	for run.Err() == nil {
		if e := w.step(run, t); e != nil {
			if !errors.Is(e, ErrFence) {
				observe.Logger(ctx).Warn("execution.step_failed", "error_kind", observe.ErrorKind(e))
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

// renewLease checks lease validity after acquiring the locks. Sampling the
// clock before a pool or row-lock wait could resurrect an expired lease.
func (w *Worker) renewLease(run context.Context, t *Task, stop context.CancelFunc) bool {
	err := fencedSlim(run, w.DB, t, []string{"cancel_requested"}, func(tx *gorm.DB, s *Session, current *Task) error {
		until := time.Now().Add(w.Lease)
		if err := tx.Model(current).Update("lease_until", until).Error; err != nil {
			return err
		}
		if current.CancelRequested && !t.CancelRequested {
			stop()
		}
		return nil
	})
	return err == nil
}
