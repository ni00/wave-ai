package execution

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/observe"
)

func (w *Worker) park(ctx context.Context, t *Task, state string) error {
	return fenced(ctx, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
		current.State = state
		current.Owner = ""
		current.LeaseUntil = nil
		return tx.Save(current).Error
	})
}
func (w *Worker) fail(ctx context.Context, t *Task, cause error) error {
	return fenced(ctx, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
		current.Error = cause.Error()
		current.PendingFinish = "failed"
		return tx.Save(current).Error
	})
}
func (w *Worker) finish(ctx context.Context, claim *Task, state string) error {
	var s Session
	var task Task
	wait := false
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
				return tx.Save(t).Error
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
						if e := tx.Save(&child).Error; e != nil {
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
			return tx.Save(t).Error
		}

		t.Finalizing = true
		t.PendingFinish = state
		return tx.Save(t).Error
	})
	if e != nil || wait {
		return e
	}
	if task.ParentID == "" && w.Finish != nil {
		if e := observe.Do(ctx, "workspace.finish", func(ctx context.Context) error { return w.Finish(ctx, s, task) }); e != nil {
			return w.parkUnknown(ctx, claim, e)
		}
	}
	e = fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {
		now := time.Now()
		t.State = state
		t.FinishedAt = &now
		t.Owner = ""
		t.LeaseUntil = nil
		if t.ParentID == "" {
			s.ActiveRoot = ""
			if e := tx.Save(s).Error; e != nil {
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
		}
		if e := tx.Save(t).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "task.finished", map[string]any{"state": state, "result": t.Result, "error": t.Error})
	})
	if e == nil {
		observe.Logger(ctx).Info("task.finished", "state", state)
	}
	return e
}
func (w *Worker) parkUnknown(ctx context.Context, t *Task, e error) error {
	return fenced(ctx, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
		current.State = "unknown"
		current.Error = e.Error()
		current.Owner = ""
		current.LeaseUntil = nil
		if e := tx.Save(current).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "task.unknown", map[string]any{"reason": e.Error()})
	})
}
