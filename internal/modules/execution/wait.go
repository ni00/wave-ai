package execution

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Check and park under the session lock also used by child completion, so a
// completion cannot arrive between observing children and parking the parent.
func (w *Worker) waitChildren(ctx context.Context, claim *Task, call ToolCall) error {
	return fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {
		now := time.Now()
		if call.StartedAt == nil {
			call.StartedAt = &now
		}
		children := []Task{}
		if e := tx.Where(eq("parent_id", t.ID)).Find(&children).Error; e != nil {
			return e
		}
		pending := false
		for _, child := range children {
			if !Terminal(child.State) {
				pending = true
			}
		}
		if pending {
			call.Status = "waiting_children"
			t.State = "waiting"
			t.Owner = ""
			t.LeaseUntil = nil
		} else {
			raw, e := json.Marshal(children)
			if e != nil {
				return e
			}
			call.FinishedAt = &now
			call.Status = "completed"
			call.Result = string(raw)
		}
		if e := tx.Save(&call).Error; e != nil {
			return e
		}
		if e := tx.Save(t).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "agent.wait", map[string]any{"call_id": call.ID, "pending": pending})
	})
}
