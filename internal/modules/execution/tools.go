package execution

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/observe"
)

func (w *Worker) tool(ctx context.Context, claim *Task, s Session, call ToolCall) error {
	if call.Tool.Kind == "agent" && call.Tool.Name == "agent_wait" {
		return w.waitChildren(ctx, claim, call)
	}
	writing := call.Tool.Kind == "builtin" && call.Tool.Name != "read" && call.Tool.Name != "ls" && call.Tool.Name != "grep" && call.Tool.Name != "find"
	blocked := false
	if e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {
		if t.CancelRequested {
			return context.Canceled
		}
		if writing {
			if s.WriterCall != "" && s.WriterCall != call.ID {
				blocked = true
				return nil
			}
			s.WriterCall = call.ID
			if e := tx.Save(s).Error; e != nil {
				return e
			}
		}
		call.Status = "running"
		now := time.Now()
		call.StartedAt = &now
		return tx.Save(&call).Error
	}); e != nil {
		return e
	}
	if blocked {
		return w.park(ctx, claim, "queued")
	}
	scope := observe.From(ctx)
	scope.SpanID = call.ID
	ctx = observe.With(ctx, scope)
	observe.Logger(ctx).Info("tool.started", "tool", call.Tool.Name)
	var result ToolResult
	var err error
	if call.Tool.Kind == "agent" {
		result, err = w.agentTool(ctx, claim, s, call)
	} else if w.Execute != nil {
		result, err = w.Execute(ctx, s, call)
	} else {
		result = ToolFailed("executor_unavailable", "tool executor not configured")
	}
	observe.Logger(ctx).Info("tool.returned", "tool", call.Tool.Name, "is_error", result.IsError, "outcome_unknown", err != nil, "duration_ms", time.Since(*call.StartedAt).Milliseconds())
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return fenced(cleanup, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {

		if err != nil {
			call.Status = "unknown"
			call.Result = err.Error()
			t.State = "unknown"
			t.Error = err.Error()
			t.Owner = ""
			t.LeaseUntil = nil
		} else {
			call.Status = "completed"
			call.Result = result.Output
			call.IsError = result.IsError
			call.ErrorCode = result.ErrorCode
			now := time.Now()
			call.FinishedAt = &now
			if writing {
				s.WriterCall = ""
				if e := tx.Save(s).Error; e != nil {
					return e
				}
			}
		}
		if e := tx.Save(&call).Error; e != nil {
			return e
		}
		if e := tx.Save(t).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "tool.result", map[string]any{"call_id": call.ID, "status": call.Status, "outcome": call.Outcome(), "result": call.Result, "is_error": call.IsError, "error_code": call.ErrorCode})
	})
}
func findTool(t *Task, name string) (agents.Tool, bool) {
	for _, tool := range t.Snapshot.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	if strings.HasPrefix(name, "agent_") {
		for _, d := range management(t) {
			if d.Function.Name == name {
				return agents.Tool{Name: name, Kind: "agent"}, true
			}
		}
	}
	return agents.Tool{Name: name}, false
}
func definitions(t Task) []modelclient.ToolDef {
	out := management(&t)
	for _, tool := range t.Snapshot.Tools {
		p := tool.Parameters
		if tool.Kind == "builtin" {
			p = agents.BuiltinParameters(tool.Name)
		}
		if p == nil {
			p = map[string]any{"type": "object", "additionalProperties": true}
		}
		out = append(out, modelclient.ToolDef{Type: "function", Function: modelclient.FuncSpec{Name: tool.Name, Description: tool.Description, Parameters: p}})
	}
	return out
}
