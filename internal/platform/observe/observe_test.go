package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestCorrelatedJSONAndPanic(t *testing.T) {
	old := slog.Default()
	defer slog.SetDefault(old)
	var out bytes.Buffer
	if err := Configure(&out, "INFO"); err != nil {
		t.Fatal(err)
	}
	var spans []Span
	ctx := With(context.Background(), Scope{TraceID: "root", TaskID: "task", SessionID: "session", Record: func(_ context.Context, s Span) { spans = append(spans, s) }})
	err := Do(ctx, "prepare", func(context.Context) error { return errors.New("SECRET-token-in-error") })
	if err == nil || len(spans) != 2 || spans[0].FinishedAt != nil || spans[1].State != "failed" {
		t.Fatal(spans)
	}
	if strings.Contains(out.String(), "SECRET") {
		t.Fatal("error content leaked")
	}
	var row map[string]any
	if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &row) != nil || row["trace_id"] != "root" || row["task_id"] != "task" {
		t.Fatal(out.String())
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		Do(ctx, "panic", func(context.Context) error { panic("SECRET") })
	}()
	if spans[len(spans)-1].State != "failed" {
		t.Fatal(spans)
	}
	_, end := Start(ctx, "once")
	end(nil)
	n := len(spans)
	end(errors.New("late"))
	if len(spans) != n {
		t.Fatal("double completion")
	}
}
