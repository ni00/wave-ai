package execution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/guardrails"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/model"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

type Model interface {
	Stream(context.Context, model.Request, func(model.Delta)) (*model.Final, error)
}
type Worker struct {
	WriteLog      func(observe.Log)
	Guard         *guardrails.Set
	ContextTokens int
	DB            *gorm.DB
	Model         Model
	Lease         time.Duration
	Execute       func(context.Context, Session, ToolCall) (ToolResult, error)
	Prepare       func(context.Context, Session, Task) error
	Finish        func(context.Context, Session, Task) error
	Evaluate      func(context.Context, Session, Task) *Evaluation
	Admit         Admission
	// Wake is the LISTEN/NOTIFY signal for newly claimable tasks; a committed
	// enqueue claims within microseconds instead of waiting for the poll
	// fallback. Nil (or a signaler that went silent) degrades the worker to
	// pure polling.
	Wake <-chan struct{}
	// DeltaFlushBytes and DeltaFlushMS bound the streamed-text buffer: deltas
	// batch until the buffer holds DeltaFlushBytes or the newest delta is
	// DeltaFlushMS older than the last flush. Zero values use the defaults
	// (4 KiB, 500 ms), tuned so a fast stream persists roughly every handful
	// of tokens while a slow one still emits mid-generation.
	DeltaFlushBytes int
	DeltaFlushMS    int
	// claim overrides the claim implementation; tests use it to observe poll
	// cadence. Nil runs the canonical claimWithAdmission.
	claim func(context.Context, *gorm.DB, string, time.Duration, Admission) (*Task, error)
}

func (w *Worker) Run(ctx context.Context) {
	if w.Lease == 0 {
		w.Lease = 30 * time.Second
	}
	owner := xid.New("worker")
	// The wake signal is the fast path; the timer is the fallback for lost
	// signals and signaling outages. An idle worker doubles its poll interval
	// from 200 ms to the 1 s cap so a fleet of workers stops rescanning an
	// empty queue at full rate, and the ±20% jitter keeps their scans from
	// synchronizing into request storms.
	poll := newPollBackoff(200*time.Millisecond, time.Second)
	defer poll.stop()
	claimFn := w.claim
	if claimFn == nil {
		claimFn = claimWithAdmission
	}
	for {
		woke := false
		select {
		case <-ctx.Done():
			return
		case <-w.Wake:
			woke = true
		case <-poll.channel():
		}
		t, e := claimFn(ctx, w.DB, owner, w.Lease, w.Admit)
		if e != nil {
			slog.Error("task.claim_failed", "error_kind", observe.ErrorKind(e))
			// A failing database should not be hammered at the base rate.
			poll.advance()
			poll.arm()
			continue
		}
		if t == nil {
			// Only a timeout-triggered empty poll grows the interval: wake
			// races must not decay the fallback, because the poller is what
			// recovers a lost notification.
			if !woke {
				poll.advance()
			}
			poll.arm()
			continue
		}
		poll.reset()
		poll.arm()
		w.run(ctx, t)
	}
}
func (w *Worker) run(ctx context.Context, t *Task) {
	ctx = w.traceContext(ctx, t)
	observe.Logger(ctx).Info("task.claimed", "epoch", t.Epoch, "worker_id", t.Owner)
	defer func() { observe.Logger(ctx).Info("task.released", "epoch", t.Epoch) }()
	run, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer cancel()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
			}
			if !w.renewLease(run, t, cancel) {
				return
			}
		}
	})
	defer func() {
		cancel()
		wg.Wait()
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_ = fenced(cleanup, w.DB, t, func(tx *gorm.DB, s *Session, current *Task) error {
			if e := interruptGenerations(tx, current); e != nil {
				return e
			}
			var running int64
			if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Count(&running).Error; e != nil {
				return e
			}
			current.State = "queued"
			if current.Finalizing {
				current.State = "unknown"
				current.Error = "finalization interrupted; confirm external operations stopped before reconciliation"
			}
			if running > 0 {
				current.State = "unknown"
				current.Error = "worker interrupted during tool execution"
				if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Update("status", "unknown").Error; e != nil {
					return e
				}
			}
			current.Owner = ""
			current.LeaseUntil = nil
			if e := tx.Save(current).Error; e != nil {
				return e
			}
			if current.State == "unknown" {
				return emit(tx, s, t.ID, "task.unknown", map[string]any{"reason": current.Error})
			}
			if e := notifyQueued(tx); e != nil {
				return e
			}
			return emit(tx, s, t.ID, "generation.interrupted", map[string]any{"attempt": current.Attempts})
		})
		if rec := recover(); rec != nil {
			observe.Logger(ctx).Error("worker.panic", "panic", fmt.Sprintf("%v", rec), "stack", string(debug.Stack()))
		}
	}()
	for run.Err() == nil {
		if e := w.step(run, t); e != nil {
			if !errors.Is(e, ErrFence) {
				observe.Logger(ctx).Warn("execution.step_failed", "error_kind", observe.ErrorKind(e))
			}
			return
		}
		var current Task
		if e := w.DB.WithContext(run).Select("state").Where(eq("id", t.ID)).Take(&current).Error; e != nil || current.State != "running" {
			return
		}
	}

}
func principal(s Session) *auth.Principal {
	return &auth.Principal{OrgID: s.OrgID, PrincipalID: s.OwnerID, Scope: auth.ScopeAPI}
}

const (
	// renewAttempts bounds transient-failure retries for one renewal slot.
	renewAttempts = 3
	// renewRetryDelay spaces renewal retries; short, because the lease clock
	// keeps running and the validity margin is what ultimately gates retries.
	renewRetryDelay = 100 * time.Millisecond
)

// renewLease checks lease validity after acquiring the locks. Sampling the
// clock before a pool or row-lock wait could resurrect an expired lease.
//
// Transient failures (connection blips, pool exhaustion) are retried, but
// only while the claim provably still holds a live lease with a safety
// margin; retries never resurrect an expired lease, and exhausted retries are
// reported as lease loss so the renew loop stops exactly as a fencing error
// would. The authoritative validity decision always belongs to the fenced
// transaction, never to the retry heuristic.
func (w *Worker) renewLease(run context.Context, t *Task, stop context.CancelFunc) bool {
	for i := 0; ; i++ {
		err := fencedSlim(run, w.DB, t, []string{"cancel_requested"}, func(tx *gorm.DB, s *Session, current *Task) error {
			until := time.Now().Add(w.Lease)
			if err := tx.Model(current).Update("lease_until", until).Error; err != nil {
				return err
			}
			if current.CancelRequested && !t.CancelRequested {
				stop()
			}
			return nil
		})
		if err == nil {
			return true
		}
		if errors.Is(err, ErrFence) {
			// Authoritative loss: the fenced check rejected the claim.
			return false
		}
		if i >= renewAttempts-1 {
			// Retries exhausted: conservatively treat the lease as lost. If
			// it is in fact still live, later steps keep succeeding until a
			// later renewal lands; claiming otherwise risks a zombie write.
			return false
		}
		// Driver-level failure with unknown outcome: retry only while the
		// stored row still shows this claim owning a lease with margin to
		// spare. A probe that cannot be read counts as an attempt rather
		// than silently extending the retry window.
		if !w.leaseLive(run, t) {
			return false
		}
		select {
		case <-run.Done():
			return false
		case <-time.After(renewRetryDelay):
		}
	}
}

// leaseLive reports whether the stored task row still shows this exact claim
// (owner+epoch) running with a lease that outlives the retry margin. The
// margin is a quarter of the lease: renewals run every second against leases
// of tens of seconds, so a healthy claim always clears it, while a worker
// stalled past the margin stops retrying before the lease legally expires.
func (w *Worker) leaseLive(ctx context.Context, t *Task) bool {
	var probe Task
	if e := w.DB.WithContext(ctx).Select("owner", "epoch", "state", "lease_until").Where(eq("id", t.ID)).Take(&probe).Error; e != nil {
		return false
	}
	return probe.Owner == t.Owner && probe.Epoch == t.Epoch && probe.State == "running" &&
		probe.LeaseUntil != nil && probe.LeaseUntil.After(time.Now().Add(w.Lease/4))
}

// pollBackoff sequences the fallback claim polls: exponential growth from
// base to cap, ±20% jitter per interval so idle workers do not synchronize,
// and a reset on success. Jitter is injectable to make the sequence
// deterministic under test.
type pollBackoff struct {
	base, ceiling time.Duration
	level         time.Duration
	jitter        func() float64
	timer         *time.Timer
}

func newPollBackoff(base, ceiling time.Duration) *pollBackoff {
	return &pollBackoff{base: base, ceiling: ceiling, level: base, jitter: rand.Float64, timer: time.NewTimer(time.Nanosecond)}
}

// delay returns the jittered interval for the current level: level scaled
// uniformly into [0.8, 1.2] × level.
func (b *pollBackoff) delay() time.Duration {
	return time.Duration(float64(b.level) * (0.8 + 0.4*b.jitter()))
}

func (b *pollBackoff) advance() { b.level = min(b.level*2, b.ceiling) }
func (b *pollBackoff) reset()   { b.level = b.base }

// arm restarts the timer for one interval, draining a pending fire so the
// timer can never deliver a stale tick.
func (b *pollBackoff) arm() {
	if !b.timer.Stop() {
		select {
		case <-b.timer.C:
		default:
		}
	}
	b.timer.Reset(b.delay())
}

func (b *pollBackoff) channel() <-chan time.Time { return b.timer.C }
func (b *pollBackoff) stop()                     { b.timer.Stop() }
