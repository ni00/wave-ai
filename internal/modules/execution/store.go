package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/webhooks"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/database"
)

var ErrPending = errors.New("workspace preparation is pending")
var ErrFence = errors.New("execution ownership lost")
var lock = clause.Locking{Strength: "UPDATE"}

func eq(k string, v any) clause.Expression { return clause.Eq{Column: k, Value: v} }
func session(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Session, error) {
	var s Session
	err := auth.Owned(db.WithContext(ctx), p).Where(eq("id", id)).Take(&s).Error
	return s, err
}
func emit(tx *gorm.DB, s *Session, task, typ string, data map[string]any) error {
	s.EventSeq++
	if err := tx.Model(s).Update("event_seq", s.EventSeq).Error; err != nil {
		return err
	}
	if err := tx.Create(&Event{SessionID: s.ID, Seq: s.EventSeq, TaskID: task, Type: typ, Data: data}).Error; err != nil {
		return err
	}
	// Wake SSE streams watching this session. The notification is emitted on
	// the same transaction as the event row, so PostgreSQL delivers it only
	// after commit: streams never chase events that rolled back. Payload is
	// the session id so each stream filters for itself.
	if err := database.NotifySessionEvent(tx, s.ID); err != nil {
		return err
	}
	payload := map[string]any{"session_id": s.ID, "task_id": task}
	for _, key := range []string{"state", "status", "call_id"} {
		if value, ok := data[key]; ok {
			payload[key] = value
		}
	}
	return webhooks.Enqueue(tx, principal(*s), fmt.Sprintf("%s:%d", s.ID, s.EventSeq), typ, payload)
}

// withTask always locks session before task; all state writers follow this order.
func withTask(ctx context.Context, db *gorm.DB, id string, fn func(*gorm.DB, *Session, *Task) error) error {
	var hint Task
	if err := db.WithContext(ctx).Select("session_id").Where(eq("id", id)).Take(&hint).Error; err != nil {
		return err
	}
	return lockTask(ctx, db, hint.SessionID, id, fn)
}

// lockTask runs fn with the session and task rows locked in the canonical
// order. sessionID must come from a durable source: the task row itself or the
// claim the worker holds.
func lockTask(ctx context.Context, db *gorm.DB, sessionID, id string, fn func(*gorm.DB, *Session, *Task) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s Session
		var t Task
		if err := tx.Clauses(lock).Where(eq("id", sessionID)).Take(&s).Error; err != nil {
			return err
		}
		if err := tx.Clauses(lock).Where(eq("id", id)).Take(&t).Error; err != nil {
			return err
		}
		return fn(tx, &s, &t)
	})
}
func authorize(s *Session, p *auth.Principal) error {
	if s.OrgID != p.OrgID || s.OwnerID != p.PrincipalID {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func fenced(ctx context.Context, db *gorm.DB, claim *Task, fn func(*gorm.DB, *Session, *Task) error) error {
	// The claim pins the session, so the task hint lookup is unnecessary here.
	return lockTask(ctx, db, claim.SessionID, claim.ID, func(tx *gorm.DB, s *Session, t *Task) error {
		if t.Owner != claim.Owner || t.Epoch != claim.Epoch || t.State != "running" || t.LeaseUntil == nil || !t.LeaseUntil.After(time.Now()) {
			return ErrFence
		}
		return fn(tx, s, t)
	})
}

// fencedSlim is fenced with a column-pruned task read, for closures that
// touch only the listed columns: full-row reads deserialize the message and
// snapshot JSONB on every call. The ownership-check columns are always
// included. Closures that Save the task must use fenced, not fencedSlim.
func fencedSlim(ctx context.Context, db *gorm.DB, claim *Task, cols []string, fn func(*gorm.DB, *Session, *Task) error) error {
	all := make([]string, 0, len(cols)+5)
	all = append(all, cols...)
	for _, c := range []string{"id", "owner", "epoch", "state", "lease_until"} {
		found := false
		for _, have := range all {
			if have == c {
				found = true
				break
			}
		}
		if !found {
			all = append(all, c)
		}
	}
	// The claim pins the session, so the task hint lookup is unnecessary here.
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s Session
		var t Task
		if err := tx.Clauses(lock).Where(eq("id", claim.SessionID)).Take(&s).Error; err != nil {
			return err
		}
		if err := tx.Clauses(lock).Select(all).Where(eq("id", claim.ID)).Take(&t).Error; err != nil {
			return err
		}
		if t.Owner != claim.Owner || t.Epoch != claim.Epoch || t.State != "running" || t.LeaseUntil == nil || !t.LeaseUntil.After(time.Now()) {
			return ErrFence
		}
		return fn(tx, &s, &t)
	})
}

func Events(ctx context.Context, db *gorm.DB, sid string, after int64) ([]Event, error) {
	rows := []Event{}
	e := db.WithContext(ctx).Where(clause.And(eq("session_id", sid), clause.Gt{Column: "seq", Value: after})).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Limit(200).Find(&rows).Error
	return rows, e
}
