package execution

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
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
		// Bounded best-effort writes: telemetry failure must not fail/retry
		// a tool. Spans buffer in the worker's span log and reach the events
		// table in batches (recorder flush or inline drain); overflow or a
		// failed batch drops spans, leaving an interrupted span in the trace.
		w.spanLog().record(ctx, t, span)
	}
	return observe.With(ctx, s)
}

// Span batching. Profiling showed one fenced transaction per span (four or
// more per task) dominating inter-span latency, so spans buffer in a bounded
// per-worker log and reach the events table in batches: a background
// recorder flushes periodically or at a count threshold, and the paths that
// release a claim (park, finish, worker cleanup) drain the buffer inside the
// transaction they already run. Both consumers take from the same
// mutex-guarded buffer, so arrival order survives: a drain first waits for
// any in-flight recorder batch of its session to commit, then the drained
// spans follow at higher sequence numbers. trace.span remains an ordinary
// event with unchanged seq semantics; only its grouping into transactions
// changed.
const (
	// spanBufferCapacity bounds the spans buffered per worker. Overflow
	// drops and counts spans — the unbatched writer already tolerated loss.
	spanBufferCapacity = 256
	// spanFlushBatch caps one flush take: spans grouped by claim, one fenced
	// transaction per group, arrival order preserved inside the group.
	spanFlushBatch = 64
	// spanFlushEvery bounds how long a span can sit in the buffer between
	// count-triggered flushes; it is the live-visibility budget for SSE
	// consumers of trace.span events.
	spanFlushEvery = 200 * time.Millisecond
	// spanWriteTimeout bounds one recorder batch write, matching the
	// per-span budget the unbatched writer used.
	spanWriteTimeout = 750 * time.Millisecond
)

// pendingSpan is one buffered span document with the claim it was recorded
// under; data is precomputed so flushes and drains only emit.
type pendingSpan struct {
	claim *Task
	span  observe.Span
	data  map[string]any
}

type spanLog struct {
	db       *gorm.DB
	capacity int
	batch    int
	every    time.Duration

	mu       sync.Mutex
	cond     *sync.Cond
	pending  []pendingSpan
	inflight map[string]int
	dropped  int
	stopped  bool

	wake     chan struct{}
	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	started  bool
}

func newSpanLog(db *gorm.DB) *spanLog {
	l := newSpanLogWith(db, spanBufferCapacity, spanFlushBatch, spanFlushEvery)
	l.start()
	return l
}

// newSpanLogWith builds a log without starting the recorder; tests drive
// flush directly or exercise inline drains only.
func newSpanLogWith(db *gorm.DB, capacity, batch int, every time.Duration) *spanLog {
	l := &spanLog{
		db:       db,
		capacity: capacity,
		batch:    batch,
		every:    every,
		inflight: map[string]int{},
		wake:     make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *spanLog) start() {
	l.started = true
	go l.loop()
}

// record buffers one span document. Marshaling happens here, outside every
// lock and transaction, so flushes and drains only emit.
func (l *spanLog) record(ctx context.Context, claim *Task, span observe.Span) {
	raw, _ := json.Marshal(span)
	data := map[string]any{}
	_ = json.Unmarshal(raw, &data)
	l.mu.Lock()
	if l.stopped || len(l.pending) >= l.capacity {
		l.dropped++
		dropped := l.dropped
		l.mu.Unlock()
		observe.Logger(ctx).Warn("trace.span_dropped", "name", span.Name, "dropped_total", dropped)
		return
	}
	l.pending = append(l.pending, pendingSpan{claim: claim, span: span, data: data})
	full := len(l.pending) >= l.batch
	l.mu.Unlock()
	if full {
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
}

// takeTask removes every buffered span of one task for an inline drain. It
// waits until no recorder batch of this session is in flight: that batch
// holds earlier arrivals and must commit first. Waiting before the caller
// opens its fenced transaction cannot deadlock, because the recorder holds
// no session lock at this point.
func (l *spanLog) takeTask(claim *Task) []pendingSpan {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.inflight[claim.SessionID] > 0 {
		l.cond.Wait()
	}
	kept := l.pending[:0]
	var out []pendingSpan
	for _, ps := range l.pending {
		if ps.claim.ID == claim.ID {
			out = append(out, ps)
			continue
		}
		kept = append(kept, ps)
	}
	l.pending = kept
	return out
}

func (l *spanLog) drops() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}

// loop is the recorder: periodic and count-triggered flushes until stop,
// then one final flush of whatever is left.
func (l *spanLog) loop() {
	defer close(l.done)
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			l.flush(true)
			return
		case <-l.wake:
		case <-ticker.C:
		}
		l.flush(false)
	}
}

func (l *spanLog) stop() {
	l.stopOnce.Do(func() {
		l.mu.Lock()
		l.stopped = true
		l.mu.Unlock()
		close(l.stopCh)
	})
	if l.started {
		<-l.done
	}
}

// flush writes buffered spans in batches. final drains the whole buffer
// (stop path); otherwise bursts larger than one batch keep looping so a
// wake signal cannot leave a full buffer behind until the next tick.
func (l *spanLog) flush(final bool) {
	for {
		groups := l.take(final)
		for _, g := range groups {
			l.write(g)
		}
		if len(groups) == 0 || (!final && l.backlog() < l.batch) {
			return
		}
	}
}

// take removes up to one batch (all of it when final) and returns the spans
// grouped by claim. One worker runs one task per session at a time, so a
// claim group is the session's batch; grouping by claim keeps the fence
// exact when a stale claim's spans linger after a failed drain.
func (l *spanLog) take(final bool) [][]pendingSpan {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.pending)
	if n == 0 {
		return nil
	}
	if !final && n > l.batch {
		n = l.batch
	}
	taken := make([]pendingSpan, n)
	copy(taken, l.pending)
	l.pending = append(l.pending[:0], l.pending[n:]...)
	groups := [][]pendingSpan{}
	index := map[string]int{}
	for _, ps := range taken {
		if i, ok := index[ps.claim.ID]; ok {
			groups[i] = append(groups[i], ps)
			continue
		}
		l.inflight[ps.claim.SessionID]++
		index[ps.claim.ID] = len(groups)
		groups = append(groups, []pendingSpan{ps})
	}
	return groups
}

func (l *spanLog) backlog() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending)
}

// write persists one claim group as a single fenced transaction, in arrival
// order. A failure drops the group: telemetry is best-effort, and replaying
// events after a lost fence would corrupt the new owner's sequence.
func (l *spanLog) write(group []pendingSpan) {
	defer l.release(group[0].claim.SessionID)
	claim := group[len(group)-1].claim
	write, cancel := context.WithTimeout(context.Background(), spanWriteTimeout)
	defer cancel()
	if err := fencedSlim(write, l.db, claim, nil, func(tx *gorm.DB, s *Session, t *Task) error {
		return emitSpans(tx, s, group)
	}); err != nil {
		observe.Logger(write).Warn("trace.write_failed", "name", group[0].span.Name, "spans", len(group))
	}
}

func (l *spanLog) release(sessionID string) {
	l.mu.Lock()
	l.inflight[sessionID]--
	if l.inflight[sessionID] <= 0 {
		delete(l.inflight, sessionID)
	}
	l.cond.Broadcast()
	l.mu.Unlock()
}

// emitSpans writes drained or flushed spans inside an already-fenced
// transaction, preserving arrival order. The fence of the surrounding
// transaction covers these emits; the session lock orders event_seq.
func emitSpans(tx *gorm.DB, s *Session, spans []pendingSpan) error {
	for _, ps := range spans {
		if e := spanMetrics(tx, s, ps.span); e != nil {
			return e
		}
		if e := emit(tx, s, ps.claim.ID, "trace.span", ps.data); e != nil {
			return e
		}
	}
	return nil
}

// spanMetrics charges sandbox startup telemetry for finished sandbox.ensure
// spans, exactly as the per-span writer did.
func spanMetrics(tx *gorm.DB, s *Session, span observe.Span) error {
	if span.Name != "sandbox.ensure" || span.FinishedAt == nil || span.DurationMS == nil {
		return nil
	}
	values := map[string]float64{"sandbox.startup": *span.DurationMS}
	if span.State == "failed" {
		values["sandbox.failed"] = 1
	}
	return telemetry.Enqueue(tx, s.OrgID, s.OwnerID, *span.FinishedAt, values)
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
