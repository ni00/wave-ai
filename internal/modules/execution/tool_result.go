package execution

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

// Resolve commits an approval decision or a confirmed external result. Each
// decision is durable and idempotent even after execution has moved forward.
func Resolve(ctx context.Context, db *gorm.DB, p *auth.Principal, taskID, callID string, result ToolResult, approve *bool) error {
	if !result.IsError && result.ErrorCode != "" {
		return apierr.Invalid("error_code requires is_error=true")
	}
	if approve != nil && result != (ToolResult{}) {
		return apierr.Invalid("submit either approval or result")
	}
	return withTask(ctx, db, taskID, func(tx *gorm.DB, s *Session, t *Task) error {
		if e := authorize(s, p); e != nil {
			return e
		}
		var call ToolCall
		if e := tx.Where(clause.And(eq("id", callID), eq("task_id", taskID))).Take(&call).Error; e != nil {
			return e
		}
		now := time.Now()
		if approve != nil {
			if call.Approval != nil {
				if *call.Approval != *approve {
					return apierr.New(409, apierr.InvalidRequest, "approval already committed")
				}
				return nil
			}
			if call.Status != "approval" {
				return apierr.New(409, apierr.InvalidRequest, "tool is not awaiting approval")
			}
			call.Approval = approve
			if *approve {
				call.Status = "ready"
				if call.Tool.Kind == "custom" {
					call.Status = "custom"
				}
			} else {
				call.Status = "completed"
				call.Result = "Tool permission denied"
				call.IsError = true
				call.ErrorCode = "permission_denied"
				call.FinishedAt = &now
			}
		} else {
			if call.Resolution != nil {
				if *call.Resolution != result {
					return apierr.New(409, apierr.InvalidRequest, "result already committed")
				}
				return nil
			}
			if call.Status != "custom" && call.Status != "unknown" {
				return apierr.New(409, apierr.InvalidRequest, "tool is not awaiting a result")
			}
			call.Status = "completed"
			call.Result, call.IsError, call.ErrorCode = result.Output, result.IsError, result.ErrorCode
			call.Resolution = &result
			call.FinishedAt = &now
			if s.WriterCall == call.ID {
				s.WriterCall = ""
				if e := tx.Save(s).Error; e != nil {
					return e
				}
			}
		}
		if e := tx.Save(&call).Error; e != nil {
			return e
		}
		if t.State == "waiting" || t.State == "unknown" {
			t.State = "queued"
			t.Error = ""
			if e := tx.Save(t).Error; e != nil {
				return e
			}
		}
		return emit(tx, s, t.ID, "tool.resolved", map[string]any{"call_id": call.ID, "status": call.Status, "outcome": call.Outcome(), "result": call.Result, "is_error": call.IsError, "error_code": call.ErrorCode})
	})
}
