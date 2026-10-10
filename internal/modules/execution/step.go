package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/model"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

// maxCappedOutputTokens is the largest per-request output cap sent to model
// providers; larger remaining budgets stay uncapped because providers reject
// limits above their own output ceilings.
const maxCappedOutputTokens = 8192

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
				current.Messages = append(current.Messages, model.Message{Role: "user", Content: in.Text})
				in.Consumed = true
				if e := tx.Model(&in).Update("consumed", true).Error; e != nil {
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
			return tx.Model(current).Select("messages", "context_ready", "pending_finish").Updates(current).Error
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
				t.Messages = append(t.Messages, model.Message{Role: "tool", ToolCallID: call.ModelID, Content: toolContent})
				if e := appendMessage(tx, s, t, Message{Role: "tool", Source: "tool", Text: call.Result, CallID: call.ID, IsError: call.IsError, ErrorCode: call.ErrorCode}); e != nil {
					return e
				}
				call.Delivered = true
				// Only the delivery flag and the appended message change; full-row
				// saves would rewrite the arguments and message JSONB on every
				// tool result.
				if e := tx.Model(&call).Select("delivered").Updates(&call).Error; e != nil {
					return e
				}
				return tx.Model(t).Select("messages").Updates(t).Error
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
			return w.parkUnknown(ctx, claim, fmt.Errorf("workspace prepare failed: %w", e))
		}
	}

	// Preparation may wait or consume the duration budget. Check after it
	// succeeds, and count only steps that proceed to model work.
	if e := fencedSlim(ctx, w.DB, claim, []string{"attempts", "used_tokens", "budget"}, func(tx *gorm.DB, s *Session, current *Task) error {
		var root Task
		if e := tx.Select("id", "used_tokens", "budget", "started_at").Where(eq("id", t.RootID)).Take(&root).Error; e != nil {
			return e
		}
		if current.UsedTokens >= current.Budget.MaxTokens || root.UsedTokens >= root.Budget.MaxTokens || (root.StartedAt != nil && time.Since(*root.StartedAt) > time.Duration(root.Budget.MaxSeconds)*time.Second) {
			return errors.New("task budget exhausted")
		}
		current.Attempts++
		t.Attempts = current.Attempts
		t.UsedTokens = current.UsedTokens
		return tx.Model(current).Update("attempts", current.Attempts).Error
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
	// Compaction charges child-task tokens to the root, and sibling children
	// can spend from the shared budget meanwhile, so re-read the root before
	// enforcing its ceiling.
	var root Task
	if e := w.DB.WithContext(ctx).Where(eq("id", t.RootID)).Take(&root).Error; e != nil {
		return e
	}
	if t.UsedTokens >= t.Budget.MaxTokens || root.UsedTokens >= root.Budget.MaxTokens {
		return w.fail(ctx, claim, errors.New("task budget exhausted after context compaction"))
	}
	// Bound a single generation to the remaining budget so one turn cannot
	// overshoot without limit; the cross-step checks above remain authoritative.
	// Large remainders are left unset because providers reject limits above
	// their own output ceilings.
	outputCap := min(t.Budget.MaxTokens-t.UsedTokens, root.Budget.MaxTokens-root.UsedTokens)
	if outputCap < maxCappedOutputTokens {
		outputCap = max(outputCap, 1)
	} else {
		outputCap = 0
	}
	messageID := xid.New("message")
	generation, err := w.startGeneration(ctx, claim, t, "response", messageID)
	if err != nil {
		return err
	}
	defs := definitions(t)
	deltas := w.newDeltaFlusher(ctx, claim, messageID, generation)
	final, e := w.Model.Stream(ctx, model.Request{Model: t.Snapshot.Model, Effort: t.Snapshot.Effort, Messages: t.Messages, Tools: defs, MaxOutputTokens: int(outputCap)}, func(d model.Delta) {
		if generation.FirstDeltaMS == nil {
			ms := time.Since(generation.StartedAt).Milliseconds()
			generation.FirstDeltaMS = &ms
		}
		if w.Guard != nil && !w.Guard.Empty() {
			return
		}
		deltas.add(d.Text)
	})
	generation.complete(final, e)
	deltas.flush()
	if deltas.err != nil {
		return deltas.err
	}
	if e != nil {
		if ctx.Err() != nil {
			return e
		}
		// Take before opening the transaction: an in-flight recorder batch
		// of this session must commit first, and waiting while holding the
		// session lock would deadlock.
		pending := w.takeSpans(claim)
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
			// The requeue branch releases the claim; drain the buffered
			// spans here so the trace stays complete for the retry worker.
			if err := emitSpans(tx, s, pending); err != nil {
				return err
			}
			// Only the retry columns change; a full-row save would rewrite
			// the message and snapshot JSONB on every model failure.
			if err := tx.Model(current).Select("failures", "usage_known", "error", "pending_finish", "state", "owner", "lease_until").Updates(current).Error; err != nil {
				return err
			}
			// A requeued retry is claimable again; wake the idle workers.
			if current.State == "queued" {
				if err := notifyQueued(tx); err != nil {
					return err
				}
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
		current.Messages = append(current.Messages, model.Message{Role: "assistant", Content: text, ToolCalls: final.ToolCalls})
		current.Result = text
		if e := appendMessage(tx, s, current, Message{ID: messageID, Role: "assistant", Source: "model", Text: text, ToolCalls: final.ToolCalls}); e != nil {
			return e
		}
		var root Task
		if e := tx.Select("id", "used_tokens", "usage_known", "tool_count", "budget").Where(eq("id", current.RootID)).Take(&root).Error; e != nil {
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
			if e := tx.Model(&root).Select("used_tokens", "usage_known", "tool_count").Updates(&root).Error; e != nil {
				return e
			}
		}
		if e := tx.Model(current).Select("failures", "messages", "result", "used_tokens", "usage_known", "tool_count", "pending_finish").Updates(current).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "message.completed", map[string]any{"text": text, "attempt": current.Attempts, "message_id": messageID, "generation_id": generation.ID})
	})
}

const (
	// defaultDeltaFlushBytes and defaultDeltaFlushMS back the delta buffer
	// when the worker was constructed without explicit thresholds; see
	// WAVE_DELTA_FLUSH_BYTES / WAVE_DELTA_FLUSH_MS for the operators' knobs.
	defaultDeltaFlushBytes = 4096
	defaultDeltaFlushMS    = 500
)

// deltaFlusher batches streamed model text into durable message.delta
// events, flushing when the buffer reaches flushBytes or the newest delta
// arrives flushEvery after the last flush.
//
// Design note (delta batching): text deltas accumulated inside one flush
// window are concatenated and persisted as a single message.delta event. The
// event's other fields (attempt, message_id, generation_id) are constant for
// the whole generation and the stream carries no per-token usage, so the
// merge loses nothing; consumers append message.delta text keyed by
// message_id in seq order, which concatenation preserves exactly (the SSE
// cursor semantics are unchanged). This is the conservative batching plan:
// had delta events carried unmergeable per-token fields, the fallback would
// be one fenced transaction inserting several events per flush, which keeps
// the write count bounded without per-token transactions; if such a field
// ever appears, switch flush to that shape. Each flush remains a fenced
// transaction (ownership re-checked under the session->task locks), so a
// stolen claim interrupts the stream instead of corrupting the new owner's
// event sequence.
type deltaFlusher struct {
	w          *Worker
	ctx        context.Context
	claim      *Task
	messageID  string
	generation Generation
	flushBytes int
	flushEvery time.Duration
	buf        strings.Builder
	last       time.Time
	err        error
}

func (w *Worker) newDeltaFlusher(ctx context.Context, claim *Task, messageID string, generation Generation) *deltaFlusher {
	flushBytes := w.DeltaFlushBytes
	if flushBytes <= 0 {
		flushBytes = defaultDeltaFlushBytes
	}
	flushMS := w.DeltaFlushMS
	if flushMS <= 0 {
		flushMS = defaultDeltaFlushMS
	}
	return &deltaFlusher{
		w: w, ctx: ctx, claim: claim, messageID: messageID, generation: generation,
		flushBytes: flushBytes, flushEvery: time.Duration(flushMS) * time.Millisecond,
		last: time.Now(),
	}
}

// add buffers one delta and flushes when a threshold has passed. Age is
// measured between arrivals: the provider callback cannot run a timer, so a
// stalled stream persists when the next token (or the final flush) arrives.
func (f *deltaFlusher) add(text string) {
	f.buf.WriteString(text)
	if f.buf.Len() >= f.flushBytes || time.Since(f.last) >= f.flushEvery {
		f.flush()
	}
}

// flush persists the buffered text as one merged event. A failed flush is
// sticky: later attempts are skipped so the step can surface the error
// instead of interleaving partial writes after a broken transaction.
func (f *deltaFlusher) flush() {
	if f.buf.Len() == 0 || f.err != nil {
		return
	}
	text := f.buf.String()
	f.buf.Reset()
	f.err = fencedSlim(f.ctx, f.w.DB, f.claim, []string{"attempts"}, func(tx *gorm.DB, s *Session, t *Task) error {
		return emit(tx, s, t.ID, "message.delta", map[string]any{"text": text, "attempt": t.Attempts, "message_id": f.messageID, "generation_id": f.generation.ID})
	})
	f.last = time.Now()
}
