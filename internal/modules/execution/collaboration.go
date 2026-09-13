package execution

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/adapters/modelclient"
)

func management(t *Task) []modelclient.ToolDef {
	if t.ParentID == "" && len(t.Experts) == 0 {
		return nil
	}
	specs := []struct {
		name, desc string
		fields     []string
	}{{"agent_message", "Send input to a task in this collaboration", []string{"task_id", "text"}}}
	if t.ParentID == "" {
		specs = append(specs, struct {
			name, desc string
			fields     []string
		}{"agent_spawn", "Delegate to an owned expert agent; only one level is allowed", []string{"agent_id", "text"}}, struct {
			name, desc string
			fields     []string
		}{"agent_wait", "Wait until all current children finish; yields execution without polling the model", []string{}}, struct {
			name, desc string
			fields     []string
		}{"agent_cancel", "Request cancellation of a child", []string{"task_id"}})
	}
	out := []modelclient.ToolDef{}
	for _, spec := range specs {
		props := map[string]any{}
		for _, f := range spec.fields {
			props[f] = map[string]any{"type": "string"}
		}
		if spec.name == "agent_spawn" {
			props["budget"] = map[string]any{"type": "object", "properties": map[string]any{"max_tokens": map[string]any{"type": "integer"}, "max_tool_calls": map[string]any{"type": "integer"}, "max_seconds": map[string]any{"type": "integer"}}}
		}
		out = append(out, modelclient.ToolDef{Type: "function", Function: modelclient.FuncSpec{Name: spec.name, Description: spec.desc, Parameters: map[string]any{"type": "object", "properties": props, "required": spec.fields}}})
	}
	return out
}
func (w *Worker) agentTool(ctx context.Context, claim *Task, s Session, c ToolCall) (ToolResult, error) {
	var result ToolResult
	e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {
		var e error
		result, e = w.agentOperation(ctx, tx, t, *s, c)
		return e
	})
	return result, e
}
func (w *Worker) agentOperation(ctx context.Context, db *gorm.DB, t *Task, s Session, c ToolCall) (ToolResult, error) {
	var in struct {
		AgentID string `json:"agent_id"`
		TaskID  string `json:"task_id"`
		Text    string `json:"text"`
		Budget  Budget `json:"budget"`
	}
	if err := json.Unmarshal([]byte(c.Arguments), &in); err != nil {
		return ToolFailed("invalid_arguments", "invalid arguments"), nil
	}
	p := principal(s)
	var out any
	var err error
	switch c.Tool.Name {
	case "agent_spawn":
		out, err = Spawn(ctx, db, p, t.ID, in.AgentID, in.Text, in.Budget)
	case "agent_wait":
		rows := []Task{}
		err = db.WithContext(ctx).Where(eq("parent_id", t.ID)).Find(&rows).Error
		out = rows
	case "agent_message", "agent_cancel":
		var target Task
		err = db.WithContext(ctx).Where(eq("id", in.TaskID)).Take(&target).Error
		if err == nil && (target.RootID != t.RootID || target.SessionID != s.ID) {
			err = errors.New("task outside this collaboration")
		}
		if err == nil {
			if c.Tool.Name == "agent_message" {
				if Terminal(target.State) && target.ParentID != "" {
					err = followup(ctx, db, p, in.TaskID, in.Text, t.ID)
				} else {
					err = steer(ctx, db, p, in.TaskID, in.Text, t.ID)
				}
			} else if target.ParentID != t.ID {
				err = errors.New("only direct children can be canceled")
			} else {
				err = Cancel(ctx, db, p, in.TaskID)
			}
		}
		out = "accepted"
	}
	if err != nil {
		return ToolFailed("delegation_rejected", "Delegation rejected: "+err.Error()), nil
	}
	b, e := json.Marshal(out)
	return ToolResult{Output: string(b)}, e
}
