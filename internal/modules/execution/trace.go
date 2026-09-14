package execution

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/telemetry"
)

type TraceSummary struct {
	WallMS            *float64           `json:"wall_ms" validate:"required" extensions:"x-nullable"`
	InitialQueueMS    *float64           `json:"initial_queue_ms" validate:"required" extensions:"x-nullable"`
	ModelMS           float64            `json:"model_ms" validate:"required"`
	ToolMS            float64            `json:"tool_ms" validate:"required"`
	PhaseMS           map[string]float64 `json:"phase_ms" validate:"required"`
	UnattributedMS    *float64           `json:"unattributed_ms" validate:"required" extensions:"x-nullable"`
	ModelCalls        int                `json:"model_calls" validate:"required"`
	ToolCalls         int                `json:"tool_calls" validate:"required"`
	FailedModelCalls  int                `json:"failed_model_calls" validate:"required"`
	FailedToolCalls   int                `json:"failed_tool_calls" validate:"required"`
	UsageKnown        bool               `json:"usage_known" validate:"required"`
	InputTokens       int64              `json:"input_tokens" validate:"required" format:"int64"`
	OutputTokens      int64              `json:"output_tokens" validate:"required" format:"int64"`
	CachedInputTokens int64              `json:"cached_input_tokens" validate:"required" format:"int64"`
}

// Trace is a projection of durable execution records, not a second state store.
// Summary totals cover this task only; child tasks link to their own trace views.
type Trace struct {
	Version    int            `json:"version" validate:"required"`
	TraceID    string         `json:"trace_id" validate:"required"`
	TaskID     string         `json:"task_id" validate:"required"`
	SessionID  string         `json:"session_id" validate:"required"`
	State      string         `json:"state" validate:"required"`
	Truncated  bool           `json:"truncated" validate:"required"`
	Incomplete bool           `json:"incomplete" validate:"required"`
	Summary    TraceSummary   `json:"summary" validate:"required"`
	Spans      []observe.Span `json:"spans" validate:"required"`
}

func (w *Worker) traceContext(ctx context.Context, t *Task) context.Context {
	s := observe.Scope{TraceID: t.RootID, TaskID: t.ID, SessionID: t.SessionID, SpanID: t.ID}
	if w.WriteLog != nil {
		var owner Session
		if err := w.DB.WithContext(ctx).Select("org_id", "owner_id").Where(eq("id", t.SessionID)).Take(&owner).Error; err == nil {
			s.OrgID, s.OwnerID, s.WriteLog = owner.OrgID, owner.OwnerID, w.WriteLog
		}
	}
	s.Record = func(ctx context.Context, span observe.Span) {
		// Bounded best-effort writes: telemetry failure must not fail/retry a tool.
		write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
		raw, _ := json.Marshal(span)
		data := map[string]any{}
		_ = json.Unmarshal(raw, &data)
		err := fenced(write, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
			if span.Name == "sandbox.ensure" && span.FinishedAt != nil && span.DurationMS != nil {
				values := map[string]float64{"sandbox.startup": *span.DurationMS}
				if span.State == "failed" {
					values["sandbox.failed"] = 1
				}
				if err := telemetry.Enqueue(tx, s.OrgID, s.OwnerID, *span.FinishedAt, values); err != nil {
					return err
				}
			}
			return emit(tx, s, t.ID, "trace.span", data)
		})
		if err != nil {
			observe.Logger(ctx).Warn("trace.write_failed", "name", span.Name)
		}
	}
	return observe.With(ctx, s)
}

// TaskTrace checks ownership before loading telemetry and bounds every query.
func TaskTrace(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Trace, error) {
	var result Trace
	// All execution writers lock the session first. Share-lock it after checking
	// ownership, then reload the task so the projection has consistent boundaries.
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t Task
		if err := tx.Omit("messages", "snapshot", "experts", "result", "error").Where(eq("id", id)).Take(&t).Error; err != nil {
			return err
		}
		if _, err := session(ctx, tx.Clauses(clause.Locking{Strength: "SHARE"}), p, t.SessionID); err != nil {
			return err
		}
		if err := tx.Omit("messages", "snapshot", "experts", "result", "error").Where(eq("id", id)).Take(&t).Error; err != nil {
			return err
		}
		const limit = 5000
		var gs []Generation
		var calls []ToolCall
		var events []Event
		var children []Task
		if err := tx.Omit("error").Where(eq("task_id", id)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Limit(limit + 1).Find(&gs).Error; err != nil {
			return err
		}
		if err := tx.Omit("arguments", "result", "resolution").Where(eq("task_id", id)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Limit(limit + 1).Find(&calls).Error; err != nil {
			return err
		}
		if err := tx.Where(clause.And(eq("task_id", id), eq("type", "trace.span"))).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Limit(limit*2 + 1).Find(&events).Error; err != nil {
			return err
		}
		if err := tx.Select("id", "parent_id", "state", "created_at", "finished_at").Where(eq("parent_id", id)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Limit(limit + 1).Find(&children).Error; err != nil {
			return err
		}
		result = buildTrace(t, gs[:min(len(gs), limit)], calls[:min(len(calls), limit)], events[:min(len(events), limit*2)], children[:min(len(children), limit)])
		result.Truncated = len(gs) > limit || len(calls) > limit || len(events) > limit*2 || len(children) > limit
		if result.Truncated {
			result.Incomplete = true
			result.Summary.UsageKnown = false
			result.Summary.UnattributedMS = nil
		}
		return nil
	})
	return result, err
}

func duration(start time.Time, end *time.Time) *float64 {
	if start.IsZero() || end == nil || end.Before(start) {
		return nil
	}
	ms := float64(end.Sub(start)) / float64(time.Millisecond)
	return &ms
}

func buildTrace(t Task, gs []Generation, calls []ToolCall, events []Event, children []Task) Trace {
	r := Trace{Version: 1, Incomplete: t.FinishedAt == nil, TraceID: t.RootID, TaskID: t.ID, SessionID: t.SessionID, State: t.State, Spans: []observe.Span{}, Summary: TraceSummary{PhaseMS: map[string]float64{}, UsageKnown: t.UsageKnown}}
	r.Summary.WallMS = duration(t.CreatedAt, t.FinishedAt)
	r.Summary.InitialQueueMS = duration(t.CreatedAt, t.StartedAt)
	r.Spans = append(r.Spans, observe.Span{ID: t.ID, ParentID: t.ParentID, TaskID: t.ID, Name: "task", Kind: "task", State: t.State, StartedAt: t.CreatedAt, FinishedAt: t.FinishedAt, DurationMS: r.Summary.WallMS})
	r.Spans = append(r.Spans, observe.Span{ID: t.ID + ":queue", ParentID: t.ID, TaskID: t.ID, Name: "queue.initial", Kind: "queue", State: "waiting", StartedAt: t.CreatedAt, FinishedAt: t.StartedAt, DurationMS: r.Summary.InitialQueueMS})
	if t.StartedAt != nil {
		r.Spans[1].State = "completed"
	}
	for _, g := range gs {
		span := observe.Span{ID: g.ID, ParentID: t.ID, TaskID: t.ID, Name: "model." + g.Purpose, Kind: "model", State: g.State, StartedAt: g.StartedAt, FinishedAt: g.FinishedAt, DurationMS: duration(g.StartedAt, g.FinishedAt), FirstDeltaMS: g.FirstDeltaMS, Attributes: map[string]any{"model": g.Model, "attempt": g.Attempt, "usage": g.Usage}}
		r.Spans = append(r.Spans, span)
		r.Summary.ModelCalls++
		if span.DurationMS != nil {
			r.Summary.ModelMS += *span.DurationMS
		}
		if g.State == "failed" || g.State == "interrupted" {
			r.Summary.FailedModelCalls++
		}
		if g.Usage.PromptTokens == nil || g.Usage.CompletionTokens == nil {
			r.Summary.UsageKnown = false
		}
		if g.Usage.PromptTokens != nil {
			r.Summary.InputTokens += *g.Usage.PromptTokens
		}
		if g.Usage.CompletionTokens != nil {
			r.Summary.OutputTokens += *g.Usage.CompletionTokens
		}
		if g.Usage.CacheReadTokens != nil {
			r.Summary.CachedInputTokens += *g.Usage.CacheReadTokens
		}
	}
	for _, c := range calls {
		start := c.CreatedAt
		kind := "tool_wait"
		if c.StartedAt != nil {
			start = *c.StartedAt
			kind = "tool"
		}
		if c.Tool.Name == "agent_wait" {
			kind = "tool_wait"
		}
		state := c.Status
		if c.IsError {
			state = "failed"
			r.Summary.FailedToolCalls++
		}
		span := observe.Span{ID: c.ID, ParentID: t.ID, TaskID: t.ID, Name: "tool." + c.Tool.Name, Kind: kind, State: state, StartedAt: start, FinishedAt: c.FinishedAt, DurationMS: duration(start, c.FinishedAt), Attributes: map[string]any{"tool_kind": c.Tool.Kind}}
		r.Spans = append(r.Spans, span)
		r.Summary.ToolCalls++
		if kind == "tool" && span.DurationMS != nil {
			r.Summary.ToolMS += *span.DurationMS
		}
	}
	phase := map[string]observe.Span{}
	for _, e := range events {
		var s observe.Span
		raw, _ := json.Marshal(e.Data)
		if json.Unmarshal(raw, &s) != nil || s.ID == "" || s.TaskID != t.ID || s.Kind != "phase" {
			r.Incomplete = true
			continue
		}
		phase[s.ID] = s
	}
	for _, s := range phase {
		// A dangling start has unknown end time, including after worker recovery.
		if s.FinishedAt == nil && t.State != "running" {
			s.State = "interrupted"
		}
		r.Spans = append(r.Spans, s)
		if s.ParentID == t.ID && s.DurationMS != nil {
			r.Summary.PhaseMS[s.Name] += *s.DurationMS
		}
	}
	for _, c := range children {
		r.Spans = append(r.Spans, observe.Span{ID: c.ID, ParentID: c.ParentID, TaskID: c.ID, Name: "task.child", Kind: "child", State: c.State, StartedAt: c.CreatedAt, FinishedAt: c.FinishedAt, DurationMS: duration(c.CreatedAt, c.FinishedAt)})
	}
	sort.Slice(r.Spans, func(i, j int) bool {
		if r.Spans[i].StartedAt.Equal(r.Spans[j].StartedAt) {
			return r.Spans[i].ID < r.Spans[j].ID
		}
		return r.Spans[i].StartedAt.Before(r.Spans[j].StartedAt)
	})
	var intervals [][2]time.Time
	for _, s := range r.Spans {
		if s.Kind == "child" || s.Kind == "task" {
			continue
		}
		if s.DurationMS == nil {
			r.Incomplete = true
			continue
		}
		if t.FinishedAt != nil {
			start, end := s.StartedAt, *s.FinishedAt
			if start.Before(t.CreatedAt) {
				start = t.CreatedAt
			}
			if end.After(*t.FinishedAt) {
				end = *t.FinishedAt
			}
			if end.After(start) {
				intervals = append(intervals, [2]time.Time{start, end})
			}
		}
	}
	if r.Summary.WallMS != nil && !r.Incomplete {
		// Union intervals: nested phases and concurrent work never double-count.
		var covered time.Duration
		var end time.Time
		for _, iv := range intervals {
			start := iv[0]
			if start.Before(end) {
				start = end
			}
			if iv[1].After(start) {
				covered += iv[1].Sub(start)
			}
			if iv[1].After(end) {
				end = iv[1]
			}
		}
		ms := max(0, *r.Summary.WallMS-float64(covered)/float64(time.Millisecond))
		r.Summary.UnattributedMS = &ms
	}
	return r
}
