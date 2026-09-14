// Package observe provides Wave's native, content-free execution telemetry.
// It does not require an exporter, collector or external tracing service.
package observe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"wave-ai.local/wave/internal/platform/xid"
)

type scopeKey struct{}
type Scope struct {
	RequestID string
	TraceID   string
	TaskID    string
	SessionID string
	SpanID    string
	Record    func(context.Context, Span)
}

// Span contains metadata only: never prompts, tool arguments, outputs or errors.
type Span struct {
	ID           string         `json:"id" validate:"required"`
	ParentID     string         `json:"parent_id,omitempty"`
	TaskID       string         `json:"task_id" validate:"required"`
	Name         string         `json:"name" validate:"required"`
	Kind         string         `json:"kind" validate:"required"`
	State        string         `json:"state" validate:"required"`
	StartedAt    time.Time      `json:"started_at" validate:"required" format:"date-time"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty" format:"date-time"`
	DurationMS   *float64       `json:"duration_ms" validate:"required" extensions:"x-nullable"`
	FirstDeltaMS *int64         `json:"first_delta_ms" validate:"required" extensions:"x-nullable" format:"int64"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

func With(ctx context.Context, s Scope) context.Context { return context.WithValue(ctx, scopeKey{}, s) }
func From(ctx context.Context) Scope                    { s, _ := ctx.Value(scopeKey{}).(Scope); return s }
func Logger(ctx context.Context) *slog.Logger {
	s := From(ctx)
	return slog.Default().With("request_id", s.RequestID, "trace_id", s.TraceID, "task_id", s.TaskID, "session_id", s.SessionID, "span_id", s.SpanID)
}

func Configure(out io.Writer, rawLevel string) error {
	level := slog.LevelInfo
	if rawLevel != "" {
		if err := level.UnmarshalText([]byte(rawLevel)); err != nil {
			return fmt.Errorf("WAVE_LOG_LEVEL must be DEBUG, INFO, WARN or ERROR")
		}
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})))
	return nil
}

// Start records both boundaries so an interrupted operation is never reported as
// a successful zero-duration span. End is idempotent and panic-safe via defer.
func Start(ctx context.Context, name string) (context.Context, func(error)) {
	s := From(ctx)
	span := Span{ID: xid.New("span"), ParentID: s.SpanID, TaskID: s.TaskID, Name: name, Kind: "phase", State: "running", StartedAt: time.Now().UTC()}
	if span.ParentID == "" {
		span.ParentID = s.TaskID
	}
	s.SpanID = span.ID
	ctx = With(ctx, s)
	if s.Record != nil {
		s.Record(ctx, span)
	}
	Logger(ctx).Debug("span.started", "name", name)
	var once sync.Once
	return ctx, func(err error) {
		once.Do(func() {
			now := time.Now().UTC()
			ms := float64(now.Sub(span.StartedAt)) / float64(time.Millisecond)
			span.FinishedAt, span.DurationMS, span.State = &now, &ms, "completed"
			if err != nil {
				span.Attributes = map[string]any{"error_kind": ErrorKind(err)}
				span.State = "failed"
			}
			if s.Record != nil {
				s.Record(ctx, span)
			}
			Logger(ctx).Info("span.finished", "name", name, "state", span.State, "duration_ms", ms)
		})
	}
}

func Do(ctx context.Context, name string, fn func(context.Context) error) (err error) {
	ctx, end := Start(ctx, name)
	defer func() {
		if p := recover(); p != nil {
			end(fmt.Errorf("panic"))
			panic(p)
		}
		end(err)
	}()
	return fn(ctx)
}

// ErrorKind exposes a safe category without serializing provider payloads or URLs.
func ErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return fmt.Sprintf("%T", err)
}
