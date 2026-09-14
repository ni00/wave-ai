package execution

import (
	"context"
	"log/slog"
	"time"
	"wave-ai.local/wave/internal/platform/observe"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Maintain enforces duration budgets even while tasks await input or tools.
func Maintain(ctx context.Context, db *gorm.DB) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := Expire(ctx, db); e != nil {
				slog.Error("execution maintenance", "error_kind", observe.ErrorKind(e))
			}
		}
	}
}
func Expire(ctx context.Context, db *gorm.DB) error {
	rows := []Task{}
	if e := db.WithContext(ctx).Select("id", "started_at", "budget").Where(clause.And(eq("cancel_requested", false), eq("finished_at", nil), clause.Neq{Column: "started_at", Value: nil})).Limit(1000).Find(&rows).Error; e != nil {
		return e
	}
	for _, row := range rows {
		if row.StartedAt == nil || time.Since(*row.StartedAt) < time.Duration(row.Budget.MaxSeconds)*time.Second || row.CancelRequested {
			continue
		}
		if e := withTask(ctx, db, row.ID, func(tx *gorm.DB, s *Session, root *Task) error {
			if Terminal(root.State) || root.CancelRequested {
				return nil
			}
			tasks := []Task{*root}
			if root.ParentID == "" {
				if e := tx.Where(eq("root_id", root.ID)).Find(&tasks).Error; e != nil {
					return e
				}
			}
			for _, task := range tasks {
				if Terminal(task.State) {
					continue
				}
				task.CancelRequested = true
				if task.ID == root.ID {
					task.PendingFinish = "failed"
					task.Error = "duration budget exhausted"
				}
				if task.State == "waiting" {
					task.State = "queued"
				}
				if e := tx.Save(&task).Error; e != nil {
					return e
				}
			}
			return emit(tx, s, root.ID, "budget.exhausted", map[string]any{"limit": "duration"})
		}); e != nil {
			return e
		}
	}
	return nil
}
