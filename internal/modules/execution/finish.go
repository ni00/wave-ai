package execution

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/telemetry"
)

func (w *Worker) park(ctx context.Context, t *Task, state string) error {
	// Take before opening the transaction: an in-flight recorder batch of
	// this session must commit first, and waiting while holding the session
	// lock would deadlock.
	pending := w.takeSpans(t)
	return fencedSlim(ctx, w.DB, t, nil, func(tx *gorm.DB, s *Session, current *Task) error {
		if state == "waiting" {
			// Resolve may have committed between the worker's snapshot and
			// this park; parking over it would strand a call that is ready
			// to run or whose result still needs delivery. Calls still in
			// approval/custom/unknown are legitimate waiting states. Requeue
			// so another worker picks the progressed call up.
			var progressed int64
			if e := tx.Model(&ToolCall{}).Where(clause.And(
				clause.Eq{Column: "task_id", Value: current.ID},
				clause.Or(
					clause.Eq{Column: "status", Value: "ready"},
					clause.And(clause.Eq{Column: "status", Value: "completed"}, clause.Eq{Column: "delivered", Value: false}),
				))).Count(&progressed).Error; e != nil {
				return e
			}
			if progressed > 0 {
				state = "queued"
			}
		}
		current.State = state
		current.Owner = ""
		current.LeaseUntil = nil
		// Parking releases the claim, so the buffered spans ride this
		// transaction: a later recorder flush would fail the fence.
		if e := emitSpans(tx, s, pending); e != nil {
			return e
		}
		// Only the release columns change; a full-row save would rewrite the
		// message and snapshot JSONB on every tool wait.
		if e := tx.Model(current).Select("state", "owner", "lease_until").Updates(current).Error; e != nil {
			return e
		}
		if state == "queued" {
			// Covers both the explicit requeue (pending preparation) and the
			// progressed-call requeue above: idle workers must see it now.
			return notifyQueued(tx)
		}
		return nil
	})
}
func (w *Worker) fail(ctx context.Context, t *Task, cause error) error {
	// The task stays running (the next step finishes it), so buffered spans
	// keep flowing to the finish drain; only the two columns change here.
	return fencedSlim(ctx, w.DB, t, nil, func(tx *gorm.DB, s *Session, current *Task) error {
		current.Error = cause.Error()
		current.PendingFinish = "failed"
		return tx.Model(current).Select("error", "pending_finish").Updates(current).Error
	})
}
func (w *Worker) finish(ctx context.Context, claim *Task, state string) error {
	var s Session
	var task Task
	wait := false
	// Every branch that waits or settles the task writes in one of the two
	// transactions below; the taken spans ride whichever one runs so the
	// buffer is empty before the claim is released. Taking before the
	// transaction cannot deadlock against an in-flight recorder batch.
	pending := w.takeSpans(claim)
	e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, sess *Session, t *Task) error {
		s = *sess
		task = *t
		if state == "canceled" || state == "failed" {
			if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), clause.IN{Column: "status", Values: []any{"ready", "approval", "waiting_children"}})).Updates(map[string]any{"status": "completed", "is_error": true, "result": "Task canceled before execution", "error_code": "task_canceled", "finished_at": time.Now()}).Error; e != nil {
				return e
			}
			var unknown int64
			if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), clause.IN{Column: "status", Values: []any{"running", "unknown", "custom"}})).Count(&unknown).Error; e != nil {
				return e
			}
			if unknown > 0 {
				wait = true
				t.State = "unknown"
				t.Error = "cancellation awaits tool outcome confirmation"
				t.Owner = ""
				t.LeaseUntil = nil
				if e := emitSpans(tx, sess, pending); e != nil {
					return e
				}
				return tx.Model(t).Select("state", "error", "owner", "lease_until").Updates(t).Error
			}
		}

		if t.ParentID == "" {
			children := []Task{}
			if e := tx.Where(eq("parent_id", t.ID)).Find(&children).Error; e != nil {
				return e
			}
			for _, child := range children {
				if !Terminal(child.State) {
					wait = true
					if state == "canceled" || state == "failed" {
						child.CancelRequested = true
						if child.State == "waiting" {
							child.State = "queued"
						}
						if e := tx.Model(&child).Select("cancel_requested", "state").Updates(&child).Error; e != nil {
							return e
						}
						// Cancellation cascades requeue waiting children; wake
						// a worker to process the cancel request.
						if e := notifyQueued(tx); e != nil {
							return e
						}
					}
				} else if child.State != "succeeded" && state == "succeeded" {
					state = "partial"
				}
			}
		}
		if wait {
			t.PendingFinish = state
			t.State = "waiting"
			t.Owner = ""
			t.LeaseUntil = nil
			if e := emitSpans(tx, sess, pending); e != nil {
				return e
			}
			return tx.Model(t).Select("pending_finish", "state", "owner", "lease_until").Updates(t).Error
		}

		t.Finalizing = true
		t.PendingFinish = state
		return tx.Model(t).Select("finalizing", "pending_finish").Updates(t).Error
	})
	if e != nil || wait {
		return e
	}
	if task.ParentID == "" && w.Finish != nil {
		if e := observe.Do(ctx, "workspace.finish", func(ctx context.Context) error { return w.Finish(ctx, s, task) }); e != nil {
			// Not retried automatically: reconcile through the documented
			// unknown-task workflow after fixing the underlying cause.
			return w.parkUnknown(ctx, claim, fmt.Errorf("artifact publish failed, awaiting client reconcile: %w", e))
		}
	}
	var evaluation *Evaluation
	if task.ParentID == "" && w.Evaluate != nil && (state == "succeeded" || state == "partial") {
		evaluation = w.Evaluate(ctx, s, task)
	}
	// Spans recorded by workspace.finish since the first transaction join
	// the pre-taken batch here, still in arrival order.
	pending = append(pending, w.takeSpans(claim)...)
	e = fencedSlim(ctx, w.DB, claim, []string{"parent_id", "root_id", "result", "error", "created_at"}, func(tx *gorm.DB, s *Session, t *Task) error {
		// Drain before any finish-path event: the finished task's trace is
		// complete the moment this transaction commits, with no extra
		// transaction of its own.
		if e := emitSpans(tx, s, pending); e != nil {
			return e
		}
		now := time.Now()
		t.Evaluation = evaluation
		t.State = state
		t.FinishedAt = &now
		t.Owner = ""
		t.LeaseUntil = nil
		if t.ParentID == "" {
			s.ActiveRoot = ""
			if e := tx.Model(s).Update("active_root", "").Error; e != nil {
				return e
			}
		} else {
			parent := Task{ID: t.ParentID, RootID: t.RootID}
			if e := addInput(tx, s, &parent, fmt.Sprintf("Subagent %s finished (%s): %s", t.ID, state, t.Result), "agent", t.ID); e != nil {
				return e
			}
			if e := tx.Model(&Task{}).Where(clause.And(eq("id", t.ParentID), eq("state", "waiting"))).Update("state", "queued").Error; e != nil {
				return e
			}
			// The finished child re-armed its waiting coordinator; wake a
			// worker to process the child's report immediately.
			if e := notifyQueued(tx); e != nil {
				return e
			}
		}
		if e := tx.Model(t).Select("evaluation", "state", "finished_at", "owner", "lease_until").Updates(t).Error; e != nil {
			return e
		}
		if err := telemetry.Enqueue(tx, s.OrgID, s.OwnerID, now, map[string]float64{"tasks.completed": 1, "tasks." + state: 1, "task.duration": float64(now.Sub(t.CreatedAt)) / float64(time.Millisecond)}); err != nil {
			return err
		}
		return emit(tx, s, t.ID, "task.finished", map[string]any{"state": state, "result": t.Result, "error": t.Error})
	})
	if e == nil {
		level := slog.LevelInfo
		if state == "failed" {
			level = slog.LevelError
		} else if state != "succeeded" {
			level = slog.LevelWarn
		}
		observe.Logger(ctx).Log(ctx, level, "task.finished", "state", state)
	}
	return e
}
func (w *Worker) parkUnknown(ctx context.Context, t *Task, e error) error {
	// Take before opening the transaction: an in-flight recorder batch of
	// this session must commit first, and waiting while holding the session
	// lock would deadlock.
	pending := w.takeSpans(t)
	return fencedSlim(ctx, w.DB, t, nil, func(tx *gorm.DB, s *Session, current *Task) error {
		current.State = "unknown"
		current.Error = e.Error()
		current.Owner = ""
		current.LeaseUntil = nil
		// Parking unknown releases the claim, so the buffered spans ride
		// this transaction.
		if err := emitSpans(tx, s, pending); err != nil {
			return err
		}
		// Only the release columns change; a full-row save would rewrite the
		// message and snapshot JSONB.
		if err := tx.Model(current).Select("state", "error", "owner", "lease_until").Updates(current).Error; err != nil {
			return err
		}
		return emit(tx, s, t.ID, "task.unknown", map[string]any{"reason": e.Error()})
	})
}
