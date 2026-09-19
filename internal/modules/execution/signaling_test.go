package execution

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/model"
	"wave-ai.local/wave/internal/platform/xid"
)

// fakeSignaler is the injectable notifier double: streams subscribe per
// session and signal fires a wake for exactly that session's subscribers.
type fakeSignaler struct {
	mu   sync.Mutex
	subs map[string][]chan struct{}
}

func newFakeSignaler() *fakeSignaler {
	return &fakeSignaler{subs: map[string][]chan struct{}{}}
}

func (f *fakeSignaler) Subscribe(ctx context.Context, sessionID string) <-chan struct{} {
	f.mu.Lock()
	ch := make(chan struct{}, 1)
	f.subs[sessionID] = append(f.subs[sessionID], ch)
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()
		keep := f.subs[sessionID][:0]
		for _, candidate := range f.subs[sessionID] {
			if candidate != ch {
				keep = append(keep, candidate)
			}
		}
		f.subs[sessionID] = keep
	}()
	return ch
}

func (f *fakeSignaler) signal(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs[sessionID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func TestPollBackoffSequence(t *testing.T) {
	b := newPollBackoff(200*time.Millisecond, time.Second)
	// Fixed jitter mid-point makes every delay exactly the level.
	b.jitter = func() float64 { return 0.5 }
	var got []time.Duration
	for range 5 {
		got = append(got, b.delay())
		b.advance()
	}
	want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, time.Second, time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff %d = %v want %v (full %v)", i, got[i], want[i], got)
		}
	}
	b.reset()
	if d := b.delay(); d != 200*time.Millisecond {
		t.Fatalf("reset level = %v", d)
	}
	// Jitter bounds: the injected extrema pin [0.8, 1.2] × level.
	b.advance()
	b.jitter = func() float64 { return 0 }
	if d := b.delay(); d != 400*time.Millisecond*8/10 {
		t.Fatalf("low jitter = %v", d)
	}
	b.jitter = func() float64 { return 1 }
	if d := b.delay(); d != 400*time.Millisecond*12/10 {
		t.Fatalf("high jitter = %v", d)
	}
}

func TestStreamPollAdaptiveInterval(t *testing.T) {
	p := newStreamPoll()
	if p.level != 250*time.Millisecond {
		t.Fatalf("initial level %v", p.level)
	}
	p.idle()
	p.idle()
	if p.level != time.Second {
		t.Fatalf("idle level %v", p.level)
	}
	p.idle()
	if p.level != time.Second {
		t.Fatalf("ceiling not applied: %v", p.level)
	}
	p.active()
	if p.level != 250*time.Millisecond {
		t.Fatalf("active level %v", p.level)
	}
}

// TestWorkerWakeAndPollBackoff proves the two halves of the idle-worker
// contract: without notifications an idle worker stretches its claim polls
// (bounded attempt count with growing gaps), and a wake signal claims
// immediately after a task commits.
func TestWorkerWakeAndPollBackoff(t *testing.T) {
	db, p, s := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{})
	attempts := make(chan time.Time, 64)
	w := &Worker{
		DB:    db,
		Model: fakeModel{},
		Lease: 5 * time.Second,
		Wake:  wake,
		claim: func(ctx context.Context, db *gorm.DB, owner string, lease time.Duration, admit Admission) (*Task, error) {
			select {
			case attempts <- time.Now():
			default:
			}
			return claimWithAdmission(ctx, db, owner, lease, admit)
		},
	}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// Idle phase: no tasks, no notifications. Within 900 ms only the
	// immediate startup attempt plus the 200/400/800 ms fallback fires
	// (at most 4 attempts), and consecutive attempts stay at least 150 ms
	// apart — a fixed 200 ms ticker would produce 5 attempts, an
	// unbounded loop far more.
	deadline := time.Now().Add(900 * time.Millisecond)
	var stamps []time.Time
	for time.Now().Before(deadline) {
		select {
		case at := <-attempts:
			stamps = append(stamps, at)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if len(stamps) < 2 {
		t.Fatalf("poll fallback never fired: %d attempts", len(stamps))
	}
	for i := 1; i < len(stamps); i++ {
		if gap := stamps[i].Sub(stamps[i-1]); gap < 150*time.Millisecond {
			t.Fatalf("attempt %d gap %v too tight: %v", i, gap, stamps)
		}
	}
	if len(stamps) > 4 {
		t.Fatalf("idle worker polled %d times in 900ms; backoff not in effect: %v", len(stamps), stamps)
	}

	// Wake phase: enqueue a task and fire the signal. The claim must run
	// well inside the smallest fallback interval.
	a := agent(t, db, p, "", nil)
	before := len(attempts)
	x := task(t, db, p, s, a)
	select {
	case wake <- struct{}{}:
	default:
		t.Fatal("wake channel blocked")
	}
	claimedBy := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(claimedBy) {
		select {
		case <-attempts:
			// The wake-driven claim attempt arrived promptly.
			waitState(t, db, x.ID, "succeeded")
			if len(attempts) > 0 {
				t.Fatal("claim kept polling after the wake")
			}
			_ = before
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("wake signal did not produce a prompt claim attempt")
}

// TestClaimRaceSingleWinner drives concurrent claimers at one task and at a
// task set: SKIP LOCKED arbitration must hand each task to exactly one
// worker, with the winners disagreeing on nothing.
func TestClaimRaceSingleWinner(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)

	one := task(t, db, p, s, a)
	var winners atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			claimed, e := Claim(ctx, db, "racer", 5*time.Second)
			if e != nil {
				t.Error(e)
				return
			}
			if claimed != nil && claimed.ID == one.ID {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if n := winners.Load(); n != 1 {
		t.Fatalf("single task claimed by %d workers", n)
	}

	// A distinct-session task set: every task claimed exactly once.
	const set = 8
	sessions := make([]Session, set)
	for i := range set {
		sessions[i] = Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID}
		if e := db.Create(&sessions[i]).Error; e != nil {
			t.Fatal(e)
		}
	}
	ids := map[string]bool{}
	for i := range set {
		x := task(t, db, p, sessions[i], a)
		ids[x.ID] = true
	}
	t.Cleanup(func() {
		for i := range sessions {
			db.Model(&Task{}).Where(eq("session_id", sessions[i].ID)).Updates(map[string]any{"state": "canceled", "owner": "", "lease_until": nil})
			db.Model(&Session{}).Where(eq("id", sessions[i].ID)).Update("active_root", "")
		}
	})
	var mu sync.Mutex
	claimed := map[string]int{}
	for range set {
		wg.Go(func() {
			for {
				c, e := Claim(ctx, db, "racer", 5*time.Second)
				if e != nil {
					t.Error(e)
					return
				}
				if c == nil {
					return
				}
				mu.Lock()
				claimed[c.ID]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(claimed) != set {
		t.Fatalf("claimed %d distinct tasks want %d", len(claimed), set)
	}
	for id, n := range claimed {
		if n != 1 || !ids[id] {
			t.Fatalf("task %s claimed %d times", id, n)
		}
	}
}

func TestRenewLeaseRetriesTransientFailures(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "renewer", 5*time.Second)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim: %v %v", claim, e)
	}
	var fail atomic.Int32
	fail.Store(2) // first two renewal attempts fail, third succeeds
	name := "test:lease-transient-retry"
	e = db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "tasks" {
			return
		}
		dest, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		if _, isLease := dest["lease_until"]; !isLease {
			return
		}
		if fail.Add(-1) >= 0 {
			tx.AddError(errors.New("injected transient renewal failure"))
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Callback().Update().Remove(name)
	w := &Worker{DB: db, Lease: 5 * time.Second}
	start := time.Now()
	if !w.renewLease(ctx, claim, func() {}) {
		t.Fatal("transient renewal failures not retried")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("retry window took %v", elapsed)
	}
	var row Task
	if e = db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	if row.LeaseUntil == nil || !row.LeaseUntil.After(time.Now()) {
		t.Fatal("lease not extended after successful retry")
	}
}

func TestRenewLeaseRetryStopsInsideMargin(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "renewer", 40*time.Second)
	if e != nil || claim == nil {
		t.Fatalf("claim: %v %v", claim, e)
	}
	// Lease still live but inside the retry margin (a quarter of the lease):
	// a transient failure here must not burn retries on a claim that can no
	// longer clear the safety margin.
	nearly := time.Now().Add(2 * time.Second)
	if e = db.Model(&Task{}).Where(eq("id", x.ID)).Update("lease_until", nearly).Error; e != nil {
		t.Fatal(e)
	}
	var fail atomic.Int32
	fail.Store(1)
	name := "test:lease-margin"
	e = db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "tasks" {
			return
		}
		if dest, ok := tx.Statement.Dest.(map[string]any); ok {
			if _, isLease := dest["lease_until"]; isLease && fail.Add(-1) >= 0 {
				tx.AddError(errors.New("injected transient renewal failure"))
			}
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Callback().Update().Remove(name)
	w := &Worker{DB: db, Lease: 40 * time.Second}
	if w.renewLease(ctx, claim, func() {}) {
		t.Fatal("renewal retried inside the expiry margin")
	}
	var row Task
	if e = db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	// The margin-gated path must neither extend nor resurrect the lease; a
	// millisecond tolerance absorbs the microsecond round-trip through
	// PostgreSQL timestamps.
	if row.LeaseUntil == nil || row.LeaseUntil.After(nearly.Add(time.Millisecond)) || row.LeaseUntil.Before(nearly.Add(-time.Millisecond)) {
		t.Fatal("margin-gated retry mutated the lease")
	}
}

func TestRenewLeaseExhaustedRetriesTreatedAsLoss(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "renewer", 30*time.Second)
	if e != nil || claim == nil {
		t.Fatalf("claim: %v %v", claim, e)
	}
	// Persistent transient failures with a healthy lease: retries run out
	// and the renewal reports loss (fence semantics) instead of hanging.
	name := "test:lease-exhausted"
	e = db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "tasks" {
			return
		}
		if dest, ok := tx.Statement.Dest.(map[string]any); ok {
			if _, isLease := dest["lease_until"]; isLease {
				tx.AddError(errors.New("injected persistent renewal failure"))
			}
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Callback().Update().Remove(name)
	w := &Worker{DB: db, Lease: 30 * time.Second}
	start := time.Now()
	if w.renewLease(ctx, claim, func() {}) {
		t.Fatal("exhausted retries reported success")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("exhaustion took %v; retry loop unbounded", elapsed)
	}
}

// TestStreamEventsWakeDelivery drives the SSE endpoint with the injectable
// signaler: an idle stream that has stretched its poll interval still
// delivers a committed event immediately when its session is signaled.
func TestStreamEventsWakeDelivery(t *testing.T) {
	db, p, s, key := setupWithKey(t)
	a := agent(t, db, p, "", nil)
	task(t, db, p, s, a)
	signal := newFakeSignaler()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/v1", httpx.Authenticate(db))
	Register(v, db, signal)

	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/"+s.ID+"/events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	events := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "event: ") {
				select {
				case events <- strings.TrimPrefix(line, "event: "):
				default:
				}
			}
		}
	}()
	// Drain the committed backlog (task.created for the first task).
	waitEvent := func(within time.Duration, want string) {
		deadline := time.After(within)
		for {
			select {
			case got := <-events:
				if got == want {
					return
				}
			case <-deadline:
				t.Fatalf("event %q not delivered within %v", want, within)
			}
		}
	}
	waitEvent(3*time.Second, "task.created")

	// Let the stream go idle long enough for its poll interval to reach the
	// 1 s ceiling, then commit a new event and fire the wake. Delivery must
	// beat the stretched poller by a wide margin.
	time.Sleep(1100 * time.Millisecond)
	task(t, db, p, s, a)
	signal.signal(s.ID)
	start := time.Now()
	waitEvent(800*time.Millisecond, "task.created")
	if took := time.Since(start); took > 800*time.Millisecond {
		t.Fatalf("wake delivery took %v; poll fallback served it instead", took)
	}
}

// TestDeltaFlushThresholds exercises the configurable batching window: with a
// 16-byte threshold and 10-byte deltas, two deltas merge into one event, the
// remainder flushes at completion, and the concatenated text plus the final
// message keep the append-only cursor semantics.
func TestDeltaFlushThresholds(t *testing.T) {
	db, p, s := setup(t)
	ctx := context.Background()
	a := agent(t, db, p, "", nil)
	task(t, db, p, s, a)
	claim, e := Claim(ctx, db, "flusher", time.Minute)
	if e != nil || claim == nil {
		t.Fatalf("claim: %v %v", claim, e)
	}
	w := &Worker{DB: db, Model: modelFunc(func(_ context.Context, _ model.Request, d func(model.Delta)) (*model.Final, error) {
		for _, chunk := range []string{"0123456789", "abcdefghij", "rem"} {
			d(model.Delta{Text: chunk})
		}
		n := int64(3)
		return &model.Final{Content: []model.ContentBlock{{Type: "text", Text: "0123456789abcdefghijrem"}}, Usage: model.Usage{PromptTokens: &n, CompletionTokens: &n}, StopReason: "stop"}, nil
	}), DeltaFlushBytes: 16, DeltaFlushMS: 60_000}
	if e = w.step(ctx, claim); e != nil {
		t.Fatal(e)
	}
	var deltas []Event
	var completed Event
	rows, e := Events(ctx, db, s.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, ev := range rows {
		switch ev.Type {
		case "message.delta":
			deltas = append(deltas, ev)
		case "message.completed":
			completed = ev
		}
	}
	if len(deltas) != 2 {
		t.Fatalf("batched delta events = %d want 2 (merged pair + remainder)", len(deltas))
	}
	if deltas[0].Data["text"] != "0123456789abcdefghij" || deltas[1].Data["text"] != "rem" {
		t.Fatalf("merged delta text corrupted: %+v", deltas)
	}
	if completed.Data["text"] != "0123456789abcdefghijrem" {
		t.Fatalf("completed text %v", completed.Data["text"])
	}
	// Cursor semantics: delta seqs precede the completed seq in order.
	if !(deltas[0].Seq < deltas[1].Seq && deltas[1].Seq < completed.Seq) {
		t.Fatalf("event ordering broken: %d %d %d", deltas[0].Seq, deltas[1].Seq, completed.Seq)
	}
}

// TestWorkerClaimWakesOnRealNotify is the end-to-end acceptance proof of the
// signaling plane: a real LISTEN connection plus a committed pg_notify from
// CreateTask claims long before the (fully backed-off) poll fallback could.
func TestWorkerClaimWakesOnRealNotify(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, p, s, _ := setupWithKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, e := database.Listen(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	w := &Worker{DB: db, Model: fakeModel{}, Lease: 5 * time.Second, Wake: TaskQueueWake(ctx, listener)}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// Run the worker idle until its poll backoff reaches the 1 s ceiling:
	// any claim from here on can only come from the notification wake (the
	// next fallback poll is a second away).
	time.Sleep(2600 * time.Millisecond)
	a := agent(t, db, p, "", nil)
	started := time.Now()
	x := task(t, db, p, s, a)
	deadline := started.Add(600 * time.Millisecond)
	var row Task
	for time.Now().Before(deadline) {
		if e = db.Where(eq("id", x.ID)).Take(&row).Error; e != nil {
			t.Fatal(e)
		}
		if row.State != "queued" {
			if took := time.Since(started); took > 500*time.Millisecond {
				t.Fatalf("claim took %v; served by poll fallback, not the wake", took)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("committed pg_notify did not wake the idle worker within 600ms")
}
