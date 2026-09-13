package execution

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

// Reconcile is an explicit operator assertion for an uncertain setup/cleanup.
// Tool calls must have individual results before a task may continue.
func Reconcile(ctx context.Context, db *gorm.DB, p *auth.Principal, id string, stopped bool) error {
	if !stopped {
		return apierr.Invalid("confirm_stopped must explicitly confirm external operations have stopped")
	}
	return withTask(ctx, db, id, func(tx *gorm.DB, s *Session, t *Task) error {
		if e := authorize(s, p); e != nil {
			return e
		}
		if t.State != "unknown" {
			return apierr.New(409, apierr.InvalidRequest, "task is not uncertain")
		}
		var count int64
		if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", id), clause.IN{Column: "status", Values: []any{"running", "unknown", "custom"}})).Count(&count).Error; e != nil {
			return e
		}
		if count != 0 {
			return apierr.New(409, apierr.InvalidRequest, "resolve individual tool results first")
		}
		if e := tx.Model(&Workspace{}).Where(clause.And(eq("session_id", s.ID), eq("state", "preparing"))).Update("state", "").Error; e != nil {
			return e
		}
		t.State = "queued"
		t.Error = ""
		t.Finalizing = false
		if e := tx.Save(t).Error; e != nil {
			return e
		}
		return emit(tx, s, id, "task.reconciled", map[string]any{"confirmed_stopped": true})
	})
}
