package observe

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Log is an application log, independent of durable execution events. Payloads,
// headers, credentials and arbitrary error strings are deliberately not captured.
type Log struct {
	ID         string         `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID      string         `gorm:"not null;index:log_owner_time,priority:1" json:"-"`
	OwnerID    string         `gorm:"not null;index:log_owner_time,priority:2" json:"-"`
	CreatedAt  time.Time      `gorm:"index:log_owner_time,priority:3;index" json:"created_at" validate:"required" format:"date-time"`
	Level      string         `gorm:"index" json:"level" validate:"required"`
	Module     string         `json:"module" validate:"required"`
	Message    string         `json:"message" validate:"required"`
	RequestID  string         `json:"request_id,omitempty"`
	TraceID    string         `gorm:"index" json:"trace_id,omitempty"`
	TaskID     string         `gorm:"index" json:"task_id,omitempty"`
	SessionID  string         `gorm:"index" json:"session_id,omitempty"`
	SpanID     string         `gorm:"index" json:"span_id,omitempty"`
	Attributes map[string]any `gorm:"serializer:json;type:jsonb" json:"attributes" validate:"required"`
	Error      map[string]any `gorm:"serializer:json;type:jsonb" json:"error,omitempty"`
	ErrorText  string         `json:"-"`
}

func (Log) TableName() string { return "monitor_logs" }

type logHandler struct {
	base    slog.Handler
	scope   Scope
	attrs   []slog.Attr
	grouped bool
}

func (h *logHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.base.Enabled(ctx, l) }
func (h *logHandler) WithAttrs(a []slog.Attr) slog.Handler {
	c := *h
	c.base = h.base.WithAttrs(a)
	c.attrs = append(append([]slog.Attr{}, h.attrs...), a...)
	return &c
}
func (h *logHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.base = h.base.WithGroup(name)
	c.grouped = h.grouped || name != ""
	return &c
}
func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.base.Handle(ctx, r)
	if h.scope.WriteLog == nil || h.scope.OrgID == "" || h.scope.OwnerID == "" {
		return err
	}
	module, _, _ := strings.Cut(r.Message, ".")
	row := Log{OrgID: h.scope.OrgID, OwnerID: h.scope.OwnerID, CreatedAt: r.Time.UTC(), Level: strings.ToLower(r.Level.String()), Module: module, Message: r.Message, RequestID: h.scope.RequestID, TraceID: h.scope.TraceID, TaskID: h.scope.TaskID, SessionID: h.scope.SessionID, SpanID: h.scope.SpanID, Attributes: map[string]any{}}
	// Only known metadata fields cross into queryable storage. Grouped custom
	// payloads and arbitrary objects never become an accidental content log.
	add := func(a slog.Attr) bool {
		switch a.Key {
		case "method", "route", "status", "duration_ms", "first_delta_ms", "model", "attempt", "purpose", "state", "tool", "name", "epoch", "worker_id", "is_error", "outcome_unknown", "error_kind", "error_code":
			v := a.Value.Resolve()
			if ptr, ok := v.Any().(*int64); ok {
				if ptr == nil {
					return true
				}
				v = slog.Int64Value(*ptr)
			}
			if v.Kind() == slog.KindAny || v.Kind() == slog.KindGroup {
				return true
			}
			row.Attributes[a.Key] = v.Any()
			if (a.Key == "error_kind" || a.Key == "error_code") && v.String() != "" {
				if row.Error == nil {
					row.Error = map[string]any{}
				}
				row.Error[a.Key] = v.Any()
				row.ErrorText += " " + v.String()
			}
		}
		return true
	}
	if !h.grouped {
		for _, a := range h.attrs {
			add(a)
		}
		r.Attrs(add)
	}
	h.scope.WriteLog(row)
	return err
}
