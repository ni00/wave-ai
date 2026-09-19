package execution

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/telemetry"
)

func Claim(ctx context.Context, db *gorm.DB, owner string, lease time.Duration) (*Task, error) {
	return claimWithAdmission(ctx, db, owner, lease, nil)
}

// Admission runs inside the claim transaction. Returning false leaves the task
// queued and lets this worker try another session without consuming its lease.
type Admission func(*gorm.DB, Session, *Task) (bool, error)

const (
	// claimScanSessions bounds how many distinct sessions one claim call
	// inspects. Anything left over stays visible to the next poll or wake
	// signal, so the bound trades tail latency inside one call for predictable
	// claim-loop latency.
	claimScanSessions = 16
	// claimCandidates caps the tasks examined under one locked session: the
	// whole transition sequence runs while the rows are locked, so a deep
	// queue behind one session cannot stretch the transaction.
	claimCandidates = 8
)

// claimable matches tasks a worker may take: fresh queues, or running tasks
// whose lease has lapsed (worker crash or stall). Kept as a helper so the
// selection statement and the locked re-read cannot drift apart.
func claimable(now time.Time) clause.Expression {
	return clause.Or(eq("state", "queued"), clause.And(eq("state", "running"), clause.Lt{Column: "lease_until", Value: now}))
}

// claimWithAdmission selects and claims one task. Selection is a single
// indexed statement (the session owning the oldest claimable task); the claim
// then runs in one transaction that locks the session before its tasks, the
// same order every fenced writer uses, with SKIP LOCKED at each step so
// concurrent claimers pass each other instead of queueing. This replaces the
// scan-64-then-lock-each-candidate loop, which spent a query plus up to two
// lock round trips per candidate and rescanned the same rows on every idle
// poll. Sessions whose candidate set collapses under the locks (lost race,
// admission decline) are set aside structurally and the scan moves on.
func claimWithAdmission(ctx context.Context, db *gorm.DB, owner string, lease time.Duration, admit Admission) (*Task, error) {
	var skipped []any
	for range claimScanSessions {
		// One statement finds the oldest claimable task's session. The
		// predicate is advisory here: it is re-evaluated on the locked rows
		// below, so a task claimed or re-leased between the two statements
		// cannot be stolen from its live owner.
		var hint struct{ SessionID string }
		q := db.WithContext(ctx).Model(&Task{}).Select("session_id").Where(claimable(time.Now())).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).Limit(1)
		if len(skipped) > 0 {
			q = q.Where(clause.Not(clause.IN{Column: "session_id", Values: skipped}))
		}
		if e := q.Take(&hint).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return nil, e
		}
		claimed, retry, e := claimSession(ctx, db, hint.SessionID, owner, lease, admit)
		if e != nil || claimed != nil {
			return claimed, e
		}
		if !retry {
			return nil, nil
		}
		skipped = append(skipped, hint.SessionID)
	}
	return nil, nil
}

// claimSession locks one session, then its candidate tasks, and performs the
// claim transition for the first candidate that survives the checks. retry
// reports whether the caller should inspect another session.
func claimSession(ctx context.Context, db *gorm.DB, sessionID, owner string, lease time.Duration, admit Admission) (*Task, bool, error) {
	skip := clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}
	var claimed *Task
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s Session
		if e := tx.Clauses(skip).Where(eq("id", sessionID)).Take(&s).Error; e != nil {
			return e
		}
		// Refresh the clock per statement: pool waits and the selection scan
		// make any earlier sample stale, and a stale lease check could steal
		// a lease that is still live.
		candidates := []Task{}
		if e := tx.Clauses(skip).Where(clause.And(eq("session_id", sessionID), claimable(time.Now()))).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).Limit(claimCandidates).Find(&candidates).Error; e != nil {
			return e
		}
		for i := range candidates {
			t := &candidates[i]
			// Ownership validation the review wanted joined into the
			// selection: a session pinned to one active root only accepts
			// tasks of that root. Checked under the locks, where it is exact.
			if s.ActiveRoot != "" && s.ActiveRoot != t.RootID {
				continue
			}
			// The locked read above already re-verified state and lease on
			// the latest committed rows; only task-specific transitions
			// remain. A lapsed running task may carry stranded generations,
			// tool calls or finalization state that must be settled before
			// the row can be reclaimed.
			if t.State == "running" {
				if e := interruptGenerations(tx, t); e != nil {
					return e
				}
			}
			if t.State == "running" && t.Finalizing {
				t.State = "unknown"
				t.Owner = ""
				t.LeaseUntil = nil
				t.Error = "worker disappeared during finalization; confirm operations stopped before reconciliation"
				if e := tx.Save(t).Error; e != nil {
					return e
				}
				if e := emit(tx, &s, t.ID, "task.unknown", map[string]any{"reason": t.Error}); e != nil {
					return e
				}
				continue
			}
			if t.State == "running" {
				var pending int64
				if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Count(&pending).Error; e != nil {
					return e
				}
				if pending > 0 {
					if e := tx.Model(&ToolCall{}).Where(clause.And(eq("task_id", t.ID), eq("status", "running"))).Update("status", "unknown").Error; e != nil {
						return e
					}
					t.State = "unknown"
					t.Owner = ""
					t.LeaseUntil = nil
					if e := tx.Save(t).Error; e != nil {
						return e
					}
					if e := emit(tx, &s, t.ID, "task.unknown", map[string]any{"reason": "worker disappeared during a tool execution"}); e != nil {
						return e
					}
					continue
				}
				if e := emit(tx, &s, t.ID, "generation.interrupted", map[string]any{"attempt": t.Attempts}); e != nil {
					return e
				}
			}
			if t.ParentID != "" && !t.CancelRequested {
				var root Task
				if e := tx.Where(eq("id", t.RootID)).Take(&root).Error; e != nil {
					return e
				}
				var running int64
				if e := tx.Model(&Task{}).Where(clause.And(eq("parent_id", t.RootID), eq("state", "running"), clause.Neq{Column: "id", Value: t.ID})).Count(&running).Error; e != nil {
					return e
				}
				if running >= int64(root.Budget.Defaults().MaxConcurrentAgents) {
					continue
				}
			}
			if admit != nil && !t.CancelRequested && t.PendingFinish == "" {
				ok, err := admit(tx, s, t)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			s.ActiveRoot = t.RootID
			if e := tx.Model(&s).Update("active_root", s.ActiveRoot).Error; e != nil {
				return e
			}
			// Take the claim clock after the locks: a timestamp captured
			// before pool waits can precede created_at, breaking trace
			// intervals and queue.wait accounting.
			claimNow := time.Now()
			t.State = "running"
			t.Owner = owner
			t.Epoch++
			until := claimNow.Add(lease)
			t.LeaseUntil = &until
			if t.StartedAt == nil {
				t.StartedAt = &claimNow
				if err := telemetry.Enqueue(tx, s.OrgID, s.OwnerID, claimNow, map[string]float64{"queue.wait": float64(claimNow.Sub(t.CreatedAt)) / float64(time.Millisecond)}); err != nil {
					return err
				}
			}
			// Only the claim columns change, plus the fields an admission
			// callback may set when a reservation is impossible; a
			// full-row save would rewrite the message and snapshot JSON.
			if e := tx.Model(t).Select("state", "owner", "epoch", "lease_until", "started_at", "pending_finish", "error").Updates(t).Error; e != nil {
				return e
			}
			claimed = t
			return nil
		}
		return nil
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	// ErrRecordNotFound means the session row was locked by a concurrent
	// writer and was skipped whole; an empty candidate set means the queue
	// lost the race. Either way another session is worth one look.
	return claimed, claimed == nil, nil
}
