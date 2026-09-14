package execution

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type waitRequest struct {
	TaskIDs        []string `json:"task_ids"`
	Mode           string   `json:"mode"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

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
		var in waitRequest
		arguments := call.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		invalid := json.Unmarshal([]byte(arguments), &in) != nil || (in.Mode != "" && in.Mode != "all" && in.Mode != "any") || in.TimeoutSeconds < 0 || in.TimeoutSeconds > 3600 || len(in.TaskIDs) > 100
		if in.Mode == "" {
			in.Mode = "all"
		}
		selected := []Task{}
		wanted := map[string]bool{}
		for _, id := range in.TaskIDs {
			wanted[id] = true
		}
		for _, child := range children {
			if len(in.TaskIDs) == 0 || wanted[child.ID] {
				selected = append(selected, child)
				delete(wanted, child.ID)
			}
		}
		if len(wanted) > 0 {
			invalid = true
		}
		pending := false
		finished := 0
		for _, child := range selected {
			if Terminal(child.State) {
				finished++
			}
		}
		if len(selected) > 0 {
			if in.Mode == "any" {
				pending = finished == 0
			} else {
				pending = finished < len(selected)
			}
		}
		timedOut := false
		if in.TimeoutSeconds > 0 {
			deadline := call.StartedAt.Add(time.Duration(in.TimeoutSeconds) * time.Second)
			call.WaitUntil = &deadline
			timedOut = !now.Before(deadline)
			if timedOut {
				pending = false
			}
		}
		if invalid {
			pending = false
			call.IsError = true
			call.ErrorCode = "invalid_arguments"
			call.Result = "Invalid wait selection, mode or timeout"
		}
		if pending {
			call.Status = "waiting_children"
			t.State = "waiting"
			t.Owner = ""
			t.LeaseUntil = nil
		} else {
			raw, e := json.Marshal(map[string]any{"tasks": selected, "timed_out": timedOut, "mode": in.Mode})
			if e != nil {
				return e
			}
			call.FinishedAt = &now
			call.Status = "completed"
			if !invalid {
				call.Result = string(raw)
			}
			call.WaitUntil = nil
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

// WakeWaiters makes deadlines durable across worker and scheduler restarts.
func WakeWaiters(ctx context.Context, db *gorm.DB) error {
	calls := []ToolCall{}
	if e := db.WithContext(ctx).Select("id", "task_id").Where(clause.And(eq("status", "waiting_children"), clause.Lte{Column: "wait_until", Value: time.Now()})).Limit(1000).Find(&calls).Error; e != nil {
		return e
	}
	for _, call := range calls {
		if e := withTask(ctx, db, call.TaskID, func(tx *gorm.DB, s *Session, t *Task) error {
			if t.State != "waiting" {
				return nil
			}
			var current ToolCall
			if e := tx.Where(eq("id", call.ID)).Take(&current).Error; e != nil {
				return e
			}
			if current.Status != "waiting_children" || current.WaitUntil == nil || current.WaitUntil.After(time.Now()) {
				return nil
			}
			return tx.Model(t).Update("state", "queued").Error
		}); e != nil {
			return e
		}
	}
	return nil
}
