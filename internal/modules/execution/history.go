package execution

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

// Message is immutable history; Task.Messages is only a compactable model checkpoint.
// Previews are events and never appear here until an authoritative result commits.
type Message struct {
	ID           string                 `gorm:"primaryKey" json:"id" validate:"required"`
	SessionID    string                 `gorm:"uniqueIndex:message_sequence,priority:1" json:"session_id" validate:"required"`
	Seq          int64                  `gorm:"uniqueIndex:message_sequence,priority:2" json:"sequence" validate:"required" format:"int64"`
	TaskID       string                 `gorm:"index" json:"task_id" validate:"required"`
	RootID       string                 `gorm:"index" json:"root_id" validate:"required"`
	Source       string                 `json:"source" validate:"required"`
	SourceTaskID string                 `json:"source_task_id,omitempty"`
	Role         string                 `json:"role" validate:"required"`
	Text         string                 `json:"text" validate:"required"`
	ToolCalls    []modelclient.ToolCall `gorm:"serializer:json;type:jsonb" json:"tool_calls,omitempty"`
	CallID       string                 `json:"call_id,omitempty"`
	IsError      bool                   `json:"is_error,omitempty"`
	ErrorCode    string                 `json:"error_code,omitempty"`
	CreatedAt    time.Time              `json:"created_at" validate:"required" format:"date-time"`
}

// appendMessage requires the session lock and shares the caller's transaction.
func appendMessage(tx *gorm.DB, s *Session, t *Task, m Message) error {
	if m.ID == "" {
		m.ID = xid.New("message")
	}
	s.MessageSeq++
	m.SessionID, m.TaskID, m.RootID, m.Seq = s.ID, t.ID, t.RootID, s.MessageSeq
	if e := tx.Model(s).Update("message_seq", s.MessageSeq).Error; e != nil {
		return e
	}
	return tx.Create(&m).Error
}

func addInput(tx *gorm.DB, s *Session, t *Task, text, source, sourceTaskID string) error {
	in := Input{ID: xid.New("input"), TaskID: t.ID, Text: text}
	if e := tx.Create(&in).Error; e != nil {
		return e
	}
	return appendMessage(tx, s, t, Message{ID: in.ID, Role: "user", Text: text, Source: source, SourceTaskID: sourceTaskID})
}

func History(ctx context.Context, db *gorm.DB, p *auth.Principal, sid, taskID string, after int64) ([]Message, error) {
	if _, e := session(ctx, db, p, sid); e != nil {
		return nil, e
	}
	rows := []Message{}
	q := db.WithContext(ctx).Where(clause.And(eq("session_id", sid), clause.Gt{Column: "seq", Value: after}))
	if taskID != "" {
		q = q.Where(eq("task_id", taskID))
	}
	e := q.Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Limit(200).Find(&rows).Error
	return rows, e
}

func inputSource(taskID string) string {
	if taskID != "" {
		return "agent"
	}
	return "user"
}
