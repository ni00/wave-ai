package execution

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/telemetry"
)

func Claim(ctx context.Context, db *gorm.DB, owner string, lease time.Duration) (*Task, error) {
	return claimWithAdmission(ctx, db, owner, lease, nil)
}

// Admission runs inside the claim transaction. Returning false leaves the task
// queued and lets this worker try another session without consuming its lease.
type Admission func(*gorm.DB, Session, *Task) (bool, error)

func claimWithAdmission(ctx context.Context, db *gorm.DB, owner string, lease time.Duration, admit Admission) (*Task, error) {
	for offset := 0; ; offset += 64 {
		candidates := []Task{}
		now := time.Now()
		if e := db.WithContext(ctx).Select("id", "session_id", "root_id").Where(clause.Or(eq("state", "queued"), clause.And(eq("state", "running"), clause.Lt{Column: "lease_until", Value: now}))).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).Offset(offset).Limit(64).Find(&candidates).Error; e != nil {
			return nil, e
		}
		for _, hint := range candidates {
			var claimed *Task
			err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var s Session
				var t Task
				skip := clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}
				if e := tx.Clauses(skip).Where(eq("id", hint.SessionID)).Take(&s).Error; e != nil {
					return e
				}
				if s.ActiveRoot != "" && s.ActiveRoot != hint.RootID {
					return nil
				}
				if e := tx.Clauses(skip).Where(eq("id", hint.ID)).Take(&t).Error; e != nil {
					return e
				}
				if t.State != "queued" && !(t.State == "running" && t.LeaseUntil != nil && t.LeaseUntil.Before(now)) {
					return nil
				}
				if t.State == "running" {
					if e := interruptGenerations(tx, &t); e != nil {
						return e
					}
				}
				if t.State == "running" && t.Finalizing {
					t.State = "unknown"
					t.Owner = ""
					t.LeaseUntil = nil
					t.Error = "worker disappeared during finalization; confirm operations stopped before reconciliation"
					if e := tx.Save(&t).Error; e != nil {
						return e
					}
					return emit(tx, &s, t.ID, "task.unknown", map[string]any{"reason": t.Error})
				}
				if t.State == "running" {
					var pending int64
					if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Count(&pending).Error; e != nil {
						return e
					}
					if pending > 0 {
						if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Update("status", "unknown").Error; e != nil {
							return e
						}
						t.State = "unknown"
						t.Owner = ""
						t.LeaseUntil = nil
						if e := tx.Save(&t).Error; e != nil {
							return e
						}
						return emit(tx, &s, t.ID, "task.unknown", map[string]any{"reason": "worker disappeared during a tool execution"})
					}
					if e := emit(tx, &s, t.ID, "generation.interrupted", map[string]any{"attempt": t.Attempts}); e != nil {
						return e
					}
				}
				if t.ParentID != "" && !t.CancelRequested {
					var root Task
					if e := tx.Where(eq("id", t.RootID)).Take(&root).Error; e != nil {
						return e
					}
					var running int64
					if e := tx.Model(&Task{}).Where(clause.And(eq("parent_id", t.RootID), eq("state", "running"), clause.Neq{Column: "id", Value: t.ID})).Count(&running).Error; e != nil {
						return e
					}
					if running >= int64(root.Budget.Defaults().MaxConcurrentAgents) {
						return nil
					}
				}
				if admit != nil && !t.CancelRequested && t.PendingFinish == "" {
					ok, err := admit(tx, s, &t)
					if err != nil || !ok {
						return err
					}
				}
				s.ActiveRoot = t.RootID
				if e := tx.Save(&s).Error; e != nil {
					return e
				}
				t.State = "running"
				t.Owner = owner
				t.Epoch++
				until := now.Add(lease)
				t.LeaseUntil = &until
				if t.StartedAt == nil {
					t.StartedAt = &now
					if err := telemetry.Enqueue(tx, s.OrgID, s.OwnerID, now, map[string]float64{"queue.wait": float64(now.Sub(t.CreatedAt)) / float64(time.Millisecond)}); err != nil {
						return err
					}
				}
				if e := tx.Save(&t).Error; e != nil {
					return e
				}
				claimed = &t
				return nil
			})
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			if claimed != nil {
				return claimed, nil
			}
		}
		if len(candidates) < 64 {
			return nil, nil
		}
	}
}
