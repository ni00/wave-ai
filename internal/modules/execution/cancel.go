package execution

import (
	"context"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/auth"
)

func Cancel(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) error {
	return withTask(ctx, db, id, func(tx *gorm.DB, s *Session, t *Task) error {
		if e := authorize(s, p); e != nil {
			return e
		}
		tasks := []Task{*t}
		if t.ParentID == "" {
			if e := tx.Where(eq("root_id", id)).Find(&tasks).Error; e != nil {
				return e
			}
		}
		for i := range tasks {
			a := &tasks[i]
			if Terminal(a.State) {
				continue
			}
			a.CancelRequested = true
			if a.State == "queued" || a.State == "waiting" {
				a.State = "queued"
			}
			if e := tx.Save(a).Error; e != nil {
				return e
			}
		}
		return emit(tx, s, id, "cancel.requested", map[string]any{})
	})
}
