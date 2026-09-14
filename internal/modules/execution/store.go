package execution

import (
	"context"
	"errors"
	"fmt"
	"time"
	"wave-ai.local/wave/internal/modules/webhooks"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/auth"
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
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s Session
		var t Task
		if err := tx.Clauses(lock).Where(eq("id", hint.SessionID)).Take(&s).Error; err != nil {
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
	return withTask(ctx, db, claim.ID, func(tx *gorm.DB, s *Session, t *Task) error {
		if t.Owner != claim.Owner || t.Epoch != claim.Epoch || t.State != "running" || t.LeaseUntil == nil || !t.LeaseUntil.After(time.Now()) {
			return ErrFence
		}
		return fn(tx, s, t)
	})
}

func Events(ctx context.Context, db *gorm.DB, sid string, after int64) ([]Event, error) {
	rows := []Event{}
	e := db.WithContext(ctx).Where(clause.And(eq("session_id", sid), clause.Gt{Column: "seq", Value: after})).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Limit(200).Find(&rows).Error
	return rows, e
}
