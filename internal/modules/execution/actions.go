package execution

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/auth"
)

// RequiredAction is a projection of persisted state, never a second queue.
type RequiredAction struct {
	Type   string    `json:"type" validate:"required" enums:"approve_tool,submit_tool_result,confirm_tool_outcome,reconcile_task"`
	TaskID string    `json:"task_id" validate:"required"`
	CallID string    `json:"call_id,omitempty"`
	Tool   *ToolCall `json:"tool,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

func RequiredActions(ctx context.Context, db *gorm.DB, p *auth.Principal, sid string) ([]RequiredAction, error) {
	out := []RequiredAction{}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := session(ctx, tx.Clauses(lock), p, sid); e != nil {
			return e
		}
		tasks := []Task{}
		if e := tx.Select("id", "state", "error").Where(clause.And(eq("session_id", sid), eq("finished_at", nil))).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Find(&tasks).Error; e != nil {
			return e
		}
		if len(tasks) == 0 {
			return nil
		}
		ids := make([]any, 0, len(tasks))
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		calls := []ToolCall{}
		if e := tx.Where(clause.And(clause.IN{Column: "task_id", Values: ids}, clause.IN{Column: "status", Values: []any{"approval", "custom", "unknown"}})).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Find(&calls).Error; e != nil {
			return e
		}
		pending := map[string]bool{}
		for _, c := range calls {
			typ := map[string]string{"approval": "approve_tool", "custom": "submit_tool_result", "unknown": "confirm_tool_outcome"}[c.Status]
			out = append(out, RequiredAction{Type: typ, TaskID: c.TaskID, CallID: c.ID, Tool: &c})
			pending[c.TaskID] = true
		}
		for _, t := range tasks {
			if t.State == "unknown" && !pending[t.ID] {
				out = append(out, RequiredAction{Type: "reconcile_task", TaskID: t.ID, Reason: t.Error})
			}
		}
		return nil
	})
	return out, err
}
