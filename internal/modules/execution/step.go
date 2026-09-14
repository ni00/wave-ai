package execution

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

func (w *Worker) step(ctx context.Context, claim *Task) error {
	var t Task
	var s Session
	var calls []ToolCall
	if e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, sess *Session, current *Task) error {
		s = *sess
		t = *current
		if e := tx.Where(clause.And(eq("task_id", t.ID), eq("delivered", false))).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).Find(&calls).Error; e != nil {
			return e
		}
		if !t.CancelRequested && len(calls) == 0 {
			if !current.ContextReady {
				if current.ParentID == "" {
					var previous Task
					if e := tx.Where(clause.And(eq("session_id", t.SessionID), eq("parent_id", ""), clause.Neq{Column: "finished_at", Value: nil})).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).Limit(1).Find(&previous).Error; e != nil {
						return e
					}
					for i, m := range completeHistory(previous.Messages) {
						if i == 0 && m.Role == "system" {
							continue
						}
						current.Messages = append(current.Messages, m)
					}
				}
				current.ContextReady = true
			}
			inputs := []Input{}
			if e := tx.Where(clause.And(eq("task_id", t.ID), eq("consumed", false))).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Find(&inputs).Error; e != nil {
				return e
			}
			for _, in := range inputs {
				current.Messages = append(current.Messages, modelclient.Message{Role: "user", Content: in.Text})
				in.Consumed = true
				if e := tx.Save(&in).Error; e != nil {
					return e
				}
				if e := emit(tx, sess, t.ID, "input.consumed", map[string]any{"input_id": in.ID}); e != nil {
					return e
				}
			}
			if len(inputs) > 0 {
				current.PendingFinish = ""
			}
			t = *current
			return tx.Save(current).Error
		}
		return nil
	}); e != nil {
		return e
	}
	if t.CancelRequested {
		if t.PendingFinish == "failed" {
			return w.finish(ctx, claim, "failed")
		}
		return w.finish(ctx, claim, "canceled")
	}
	for _, call := range calls {
		if call.Status == "completed" {
			if e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {
				toolContent := call.Result
				if call.IsError {
					toolContent = "Tool failed (" + call.ErrorCode + "): " + call.Result
				}
				t.Messages = append(t.Messages, modelclient.Message{Role: "tool", ToolCallID: call.ModelID, Content: toolContent})
				if e := appendMessage(tx, s, t, Message{Role: "tool", Source: "tool", Text: call.Result, CallID: call.ID, IsError: call.IsError, ErrorCode: call.ErrorCode}); e != nil {
					return e
				}
				call.Delivered = true
				if e := tx.Save(&call).Error; e != nil {
					return e
				}
				return tx.Save(t).Error
			}); e != nil {
				return e
			}
			continue
		}
		if call.Status == "waiting_children" {
			return w.waitChildren(ctx, claim, call)
		}
		if call.Status == "ready" {
			return w.tool(ctx, claim, s, call)
		}
		return w.park(ctx, claim, "waiting")
	}
	if len(calls) > 0 {
		return nil
	}
	if t.PendingFinish != "" {
		return w.finish(ctx, claim, t.PendingFinish)
	}
	if w.Prepare != nil {
		if e := observe.Do(ctx, "workspace.prepare", func(ctx context.Context) error { return w.Prepare(ctx, s, t) }); e != nil {
			if errors.Is(e, ErrPending) {
				return w.park(ctx, claim, "queued")
			}
			return w.parkUnknown(ctx, claim, e)
		}
	}
	var root Task
	if e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, current *Task) error {
		if e := tx.Where(eq("id", t.RootID)).Take(&root).Error; e != nil {
			return e
		}
		if t.UsedTokens >= t.Budget.MaxTokens || root.UsedTokens >= root.Budget.MaxTokens || (root.StartedAt != nil && time.Since(*root.StartedAt) > time.Duration(root.Budget.MaxSeconds)*time.Second) {
			return errors.New("task budget exhausted")
		}
		current.Attempts++
		t = *current
		return tx.Save(current).Error
	}); e != nil {
		if errors.Is(e, ErrFence) {
			return e
		}
		return w.fail(ctx, claim, e)
	}

	if w.Guard != nil {
		for _, m := range t.Messages {
			if m.Role == "user" {
				text, _ := m.Content.(string)
				if e := w.Guard.CheckTextInput(text); e != nil {
					return w.fail(ctx, claim, e)
				}
			}
		}
	}
	var compactErr error
	t, compactErr = w.compact(ctx, claim, t)
	if compactErr != nil {
		if errors.Is(compactErr, ErrFence) {
			return compactErr
		}
		return w.fail(ctx, claim, compactErr)
	}
	if e := w.DB.WithContext(ctx).Where(eq("id", t.RootID)).Take(&root).Error; e != nil {
		return e
	}
	if t.UsedTokens >= t.Budget.MaxTokens || root.UsedTokens >= root.Budget.MaxTokens {
		return w.fail(ctx, claim, errors.New("task budget exhausted after context compaction"))
	}
	messageID := xid.New("message")
	generation, err := w.startGeneration(ctx, claim, t, "response", messageID)
	if err != nil {
		return err
	}
	defs := definitions(t)
	var delta strings.Builder
	last := time.Now()
	var deltaErr error
	flush := func() {
		if delta.Len() == 0 || deltaErr != nil {
			return
		}
		text := delta.String()
		delta.Reset()
		deltaErr = fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, t *Task) error {
			return emit(tx, s, t.ID, "message.delta", map[string]any{"text": text, "attempt": t.Attempts, "message_id": messageID, "generation_id": generation.ID})
		})
		last = time.Now()
	}
	final, e := w.Model.Stream(ctx, modelclient.Request{Model: t.Snapshot.Model, Effort: t.Snapshot.Effort, Messages: t.Messages, Tools: defs}, func(d modelclient.Delta) {
		if generation.FirstDeltaMS == nil {
			ms := time.Since(generation.StartedAt).Milliseconds()
			generation.FirstDeltaMS = &ms
		}
		if w.Guard != nil && !w.Guard.Empty() {
			return
		}
		delta.WriteString(d.Text)
		if delta.Len() >= 1024 || time.Since(last) >= 200*time.Millisecond {
			flush()
		}
	})
	generation.complete(final, e)
	flush()
	if deltaErr != nil {
		return deltaErr
	}
	if e != nil {
		if ctx.Err() != nil {
			return e
		}
		return fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, current *Task) error {
			if err := saveGeneration(tx, s, &generation); err != nil {
				return err
			}
			current.Failures++
			current.UsageKnown = false
			if current.ParentID != "" {
				if e := tx.Model(&Task{}).Where(eq("id", current.RootID)).Update("usage_known", false).Error; e != nil {
					return e
				}
			}
			if current.Failures >= 3 {
				current.Error = e.Error()
				current.PendingFinish = "failed"
			} else {
				current.State = "queued"
				current.Owner = ""
				current.LeaseUntil = nil
			}
			if err := tx.Save(current).Error; err != nil {
				return err
			}
			return emit(tx, s, current.ID, "generation.failed", map[string]any{"attempt": current.Attempts, "error": e.Error(), "generation_id": generation.ID, "message_id": messageID})
		})
	}
	return fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, current *Task) error {
		if e := saveGeneration(tx, s, &generation); e != nil {
			return e
		}
		current.Failures = 0
		text := ""
		for _, c := range final.Content {
			if c.Type == "text" {
				text += c.Text
			}
		}
		if w.Guard != nil {
			text = w.Guard.RedactOutput(text)
		}
		current.Messages = append(current.Messages, modelclient.Message{Role: "assistant", Content: text, ToolCalls: final.ToolCalls})
		current.Result = text
		if e := appendMessage(tx, s, current, Message{ID: messageID, Role: "assistant", Source: "model", Text: text, ToolCalls: final.ToolCalls}); e != nil {
			return e
		}
		var root Task
		if e := tx.Where(eq("id", current.RootID)).Take(&root).Error; e != nil {
			return e
		}
		tokens := int64(0)
		known := final.Usage.PromptTokens != nil && final.Usage.CompletionTokens != nil
		if known {
			tokens = *final.Usage.PromptTokens + *final.Usage.CompletionTokens
		}
		current.UsedTokens += tokens
		current.UsageKnown = current.UsageKnown && known
		if current.ParentID != "" {
			root.UsedTokens += tokens
			root.UsageKnown = root.UsageKnown && known
		}
		for _, tc := range final.ToolCalls {
			def, ok := findTool(current, tc.Function.Name)
			call := ToolCall{ID: xid.New("call"), TaskID: t.ID, ModelID: tc.ID, Tool: def, Arguments: tc.Function.Arguments, Status: "ready"}
			count := root.ToolCount
			if current.ParentID == "" {
				count = current.ToolCount
			}
			if !ok || count >= root.Budget.MaxToolCalls || current.ToolCount >= current.Budget.MaxToolCalls {
				call.Status = "completed"
				call.IsError = true
				call.Result = "Tool is unavailable or budget is exhausted"
				call.ErrorCode = "tool_unavailable"
				now := time.Now()
				call.FinishedAt = &now
			} else {
				if current.ParentID == "" {
					current.ToolCount++
				} else {
					root.ToolCount++
					current.ToolCount++
				}
				if def.Kind == "custom" {
					call.Status = "custom"
				}
				if def.Approval {
					call.Status = "approval"
				}
			}
			if e := tx.Create(&call).Error; e != nil {
				return e
			}
			if e := emit(tx, s, t.ID, "tool.created", map[string]any{"call_id": call.ID, "name": tc.Function.Name, "status": call.Status}); e != nil {
				return e
			}
		}
		if len(final.ToolCalls) == 0 {
			current.PendingFinish = "succeeded"
			if final.StopReason == "length" {
				current.PendingFinish = "partial"
			}
		}
		if current.ParentID != "" {
			if e := tx.Save(&root).Error; e != nil {
				return e
			}
		}
		if e := tx.Save(current).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "message.completed", map[string]any{"text": text, "attempt": current.Attempts, "message_id": messageID, "generation_id": generation.ID})
	})
}
