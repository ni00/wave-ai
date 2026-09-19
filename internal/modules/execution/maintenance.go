package execution

import (
	"context"
	"log/slog"
	"time"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/platform/observe"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Maintain enforces duration budgets even while tasks await input or tools.
func Maintain(ctx context.Context, db *gorm.DB) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := Expire(ctx, db); e != nil {
				slog.Error("execution maintenance", "error_kind", observe.ErrorKind(e))
			}
		case <-prune.C:
			if e := pruneAuxTables(ctx, db); e != nil {
				slog.Error("execution prune", "error_kind", observe.ErrorKind(e))
			}
		}
	}
}

// pruneAuxTables bounds auxiliary state with no source-of-truth value beyond
// replay windows: idempotency receipts (stored responses) and sandbox process
// bookkeeping (request-ID dedup). Task and session records are never touched.
func pruneAuxTables(ctx context.Context, db *gorm.DB) error {
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, model := range []any{&Receipt{}, &sandbox.Process{}} {
		for {
			deleted, e := pruneAuxBatch(ctx, db, model, cutoff)
			if e != nil {
				return e
			}
			if deleted == 0 {
				break
			}
		}
	}
	return nil
}

// pruneAuxBatch selects primary keys first because PostgreSQL does not apply
// LIMIT to a bulk DELETE. A timestamp alone cannot bound a batch when it ties.
func pruneAuxBatch(ctx context.Context, db *gorm.DB, model any, cutoff time.Time) (int64, error) {
	query := db.WithContext(ctx).Model(model).
		Where(clause.Lt{Column: "created_at", Value: cutoff}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).
		Limit(500)
	var keys []clause.Expression
	switch model.(type) {
	case *Receipt:
		var rows []Receipt
		if e := query.Select("principal_id", "operation", "key").Find(&rows).Error; e != nil {
			return 0, e
		}
		for _, row := range rows {
			keys = append(keys, clause.And(
				clause.Eq{Column: "principal_id", Value: row.PrincipalID},
				clause.Eq{Column: "operation", Value: row.Operation},
				clause.Eq{Column: "key", Value: row.Key},
			))
		}
	case *sandbox.Process:
		var rows []sandbox.Process
		if e := query.Select("session_id", "request_id").Find(&rows).Error; e != nil {
			return 0, e
		}
		for _, row := range rows {
			keys = append(keys, clause.And(
				clause.Eq{Column: "session_id", Value: row.SessionID},
				clause.Eq{Column: "request_id", Value: row.RequestID},
			))
		}
	}
	if len(keys) == 0 {
		return 0, nil
	}
	result := db.WithContext(ctx).Where(clause.And(
		clause.Lt{Column: "created_at", Value: cutoff},
		clause.Or(keys...),
	)).Delete(model)
	return result.RowsAffected, result.Error
}

func Expire(ctx context.Context, db *gorm.DB) error {
	if e := WakeWaiters(ctx, db); e != nil {
		return e
	}
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
				// Expiration requeues waiters so their cancellation runs;
				// wake a worker to process it.
				if e := notifyQueued(tx); e != nil {
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
