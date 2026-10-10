package execution

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/model"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

// claimOne claims the only queued task of the fixture session.
func claimOne(t *testing.T, db *gorm.DB, owner string) *Task {
	t.Helper()
	claimed, err := claimWithAdmission(context.Background(), db, owner, time.Minute, nil)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	return claimed
}

// spanEvents loads the task's trace.span events in seq order.
func spanEvents(t *testing.T, db *gorm.DB, taskID string) []Event {
	t.Helper()
	var events []Event
	if e := db.Where(clause.And(eq("task_id", taskID), eq("type", "trace.span"))).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Find(&events).Error; e != nil {
		t.Fatal(e)
	}
	return events
}

// TestFinishDrainsBufferedSpansInline drives a task to completion with no
// background recorder running (loop never started, period one hour), so the
// only possible writer is the inline drain. The trace must be complete the
// moment the task finishes: every phase present, finished, and sequenced
// before the terminal event.
func TestFinishDrainsBufferedSpansInline(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "answer", nil)
	created := task(t, db, p, s, a)
	w := &Worker{DB: db, Model: fakeModel{}, Lease: time.Minute,
		// The step and finish callers already wrap these hooks in
		// observe.Do, which records the phase spans into the buffer.
		Prepare: func(context.Context, Session, Task) error { return nil },
		Finish:  func(context.Context, Session, Task) error { return nil },
	}
	w.spans = newSpanLogWith(db, spanBufferCapacity, spanFlushBatch, time.Hour)
	claimed := claimOne(t, db, "drain-test")
	ctx := w.traceContext(context.Background(), claimed)
	for range 10 {
		var row Task
		if e := db.Where(eq("id", created.ID)).Take(&row).Error; e != nil {
			t.Fatal(e)
		}
		if row.State != "running" {
			break
		}
		if e := w.step(ctx, claimed); e != nil {
			t.Fatal(e)
		}
	}
	waitState(t, db, created.ID, "succeeded")
	r, err := TaskTrace(ctx, db, p, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, sp := range r.Spans {
		if sp.Kind != "phase" {
			continue
		}
		phases = append(phases, sp.Name)
		if sp.FinishedAt == nil || sp.State != "completed" {
			t.Fatalf("phase %s not completed: %+v", sp.Name, sp)
		}
	}
	if len(phases) != 2 || phases[0] != "workspace.prepare" || phases[1] != "workspace.finish" {
		t.Fatalf("phases: %v", phases)
	}
	if r.Incomplete || r.Summary.PhaseMS["workspace.prepare"] <= 0 || r.Summary.PhaseMS["workspace.finish"] <= 0 || r.Summary.UnattributedMS == nil {
		t.Fatalf("summary incomplete: %+v", r.Summary)
	}
	// The drain rode the terminal transaction: four span documents (start
	// and finish per phase) all precede task.finished in session order.
	spans := spanEvents(t, db, created.ID)
	var finished []Event
	if e := db.Where(clause.And(eq("task_id", created.ID), eq("type", "task.finished"))).Find(&finished).Error; e != nil {
		t.Fatal(e)
	}
	if len(spans) != 4 || len(finished) != 1 {
		t.Fatalf("events: spans=%d finished=%d", len(spans), len(finished))
	}
	for _, sp := range spans {
		if sp.Seq > finished[0].Seq {
			t.Fatalf("trace.span seq %d committed after task.finished seq %d", sp.Seq, finished[0].Seq)
		}
	}
}

// TestSpanBufferOverflowDropsAndCounts overflows a tiny buffer: recording
// never blocks or panics, the drop count is visible, and nothing reaches
// the database without a flush.
func TestSpanBufferOverflowDropsAndCounts(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "answer", nil)
	created := task(t, db, p, s, a)
	w := &Worker{DB: db}
	w.spans = newSpanLogWith(db, 8, 4, time.Hour)
	claimed := claimOne(t, db, "overflow-test")
	ctx := w.traceContext(context.Background(), claimed)
	for i := range 20 {
		if err := observe.Do(ctx, fmt.Sprintf("phase.%02d", i), func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	// 40 records (start and finish per phase) into a buffer of 8.
	if got := w.spans.drops(); got != 32 {
		t.Fatalf("drops = %d, want 32", got)
	}
	if buffered := w.spans.takeTask(claimed); len(buffered) != 8 {
		t.Fatalf("buffered = %d, want 8", len(buffered))
	}
	if again := w.spans.takeTask(claimed); len(again) != 0 {
		t.Fatalf("second take = %d, want 0", len(again))
	}
	if events := spanEvents(t, db, created.ID); len(events) != 0 {
		t.Fatalf("%d span events written without a flush", len(events))
	}
}

// TestSpanRecorderFlushOrdering runs the recorder with a small batch: the
// wake-triggered flushes keep seq strictly increasing and preserve arrival
// order, within and across batches.
func TestSpanRecorderFlushOrdering(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "answer", nil)
	created := task(t, db, p, s, a)
	w := &Worker{DB: db}
	w.spans = newSpanLogWith(db, spanBufferCapacity, 4, time.Hour)
	w.spans.start()
	t.Cleanup(w.spans.stop)
	claimed := claimOne(t, db, "recorder-test")
	ctx := w.traceContext(context.Background(), claimed)
	record := func(prefix string, n int) []string {
		t.Helper()
		var want []string
		for i := range n {
			name := fmt.Sprintf("%s.%02d", prefix, i)
			if err := observe.Do(ctx, name, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			want = append(want, name+":running", name+":completed")
		}
		return want
	}
	check := func(want []string) {
		t.Helper()
		events := spanEvents(t, db, created.ID)
		deadline := time.Now().Add(5 * time.Second)
		for len(events) < len(want) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			events = spanEvents(t, db, created.ID)
		}
		got := make([]string, 0, len(events))
		var prev int64
		for _, ev := range events {
			if ev.Seq <= prev {
				t.Fatalf("seq not strictly increasing: %d after %d", ev.Seq, prev)
			}
			prev = ev.Seq
			name, _ := ev.Data["name"].(string)
			state, _ := ev.Data["state"].(string)
			got = append(got, name+":"+state)
		}
		if len(got) != len(want) {
			t.Fatalf("got %d span events, want %d", len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("span order diverges at %d: %v want %v", i, got, want)
			}
		}
	}
	want := record("phase", 6)
	check(want)
	// A second burst after the first flushes stays ordered across batches.
	want = append(want, record("later", 6)...)
	check(want)
}

// updateColumns captures the SET column list of UPDATE statements against
// one table through a gorm callback.
func updateColumns(t *testing.T, db *gorm.DB, table string) func() []map[string]bool {
	t.Helper()
	var mu sync.Mutex
	var captured []map[string]bool
	name := "test:capture-" + table + "-updates"
	if e := db.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != table || tx.Statement.SQL.Len() == 0 {
			return
		}
		sql := tx.Statement.SQL.String()
		start := strings.Index(sql, "SET ")
		end := strings.Index(sql, " WHERE ")
		if start < 0 || end < start {
			return
		}
		cols := map[string]bool{}
		for _, m := range regexp.MustCompile(`"([a-z_]+)"\s*=`).FindAllStringSubmatch(sql[start:end], -1) {
			cols[m[1]] = true
		}
		mu.Lock()
		captured = append(captured, cols)
		mu.Unlock()
	}); e != nil {
		t.Fatal(e)
	}
	return func() []map[string]bool {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// TestSaveGenerationPrunesColumns asserts the completion update rewrites
// only the mutable generation columns, never the identity columns.
func TestSaveGenerationPrunesColumns(t *testing.T) {
	db, _, s := setup(t)
	captured := updateColumns(t, db, "generations")
	g := Generation{ID: xid.New("generation"), TaskID: xid.New("task"), MessageID: xid.New("message"), Purpose: "response", Model: "test-model", Attempt: 1, State: "running", StartedAt: time.Now()}
	if e := db.Create(&g).Error; e != nil {
		t.Fatal(e)
	}
	n := int64(3)
	g.complete(&model.Final{Usage: model.Usage{PromptTokens: &n, CompletionTokens: &n}}, nil)
	if e := db.Transaction(func(tx *gorm.DB) error { return saveGeneration(tx, &s, &g) }); e != nil {
		t.Fatal(e)
	}
	updates := captured()
	if len(updates) != 1 {
		t.Fatalf("generation updates: %d", len(updates))
	}
	cols := updates[0]
	for _, want := range []string{"state", "finished_at", "duration_ms", "first_delta_ms", "usage", "error"} {
		if !cols[want] {
			t.Fatalf("update missing %q: %v", want, cols)
		}
	}
	for _, banned := range []string{"id", "task_id", "message_id", "purpose", "model", "attempt", "started_at"} {
		if cols[banned] {
			t.Fatalf("update rewrote identity column %q", banned)
		}
	}
	var row Generation
	if e := db.Where(eq("id", g.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.State != "completed" || row.FinishedAt == nil || row.Usage.PromptTokens == nil || *row.Usage.PromptTokens != 3 {
		t.Fatalf("pruned update lost data: %+v", row)
	}
}

// TestParkPrunesTaskColumns asserts a park writes only the release columns,
// never the message or snapshot JSONB.
func TestParkPrunesTaskColumns(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "answer", nil)
	created := task(t, db, p, s, a)
	w := &Worker{DB: db}
	claimed := claimOne(t, db, "prune-test")
	captured := updateColumns(t, db, "tasks")
	if e := w.park(context.Background(), claimed, "waiting"); e != nil {
		t.Fatal(e)
	}
	updates := captured()
	if len(updates) != 1 {
		t.Fatalf("task updates during park: %d %v", len(updates), updates)
	}
	cols := updates[0]
	for _, want := range []string{"state", "owner", "lease_until"} {
		if !cols[want] {
			t.Fatalf("park update missing %q: %v", want, cols)
		}
	}
	for _, banned := range []string{"messages", "snapshot", "experts", "result", "error", "evaluation", "budget"} {
		if cols[banned] {
			t.Fatalf("park update rewrote %q: %v", banned, cols)
		}
	}
	var row Task
	if e := db.Where(eq("id", created.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.State != "waiting" || row.Owner != "" || row.LeaseUntil != nil {
		t.Fatalf("park did not release the claim: %+v", row)
	}
}
