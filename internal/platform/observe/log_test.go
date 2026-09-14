package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestScopedLogLevelCorrelationAndRedaction(t *testing.T) {
	old := slog.Default()
	defer slog.SetDefault(old)
	var out bytes.Buffer
	if err := Configure(&out, "debug"); err != nil {
		t.Fatal(err)
	}
	var rows []Log
	ctx := With(context.Background(), Scope{OrgID: "org", OwnerID: "alice", TraceID: "root", TaskID: "task", SpanID: "generation", WriteLog: func(l Log) { rows = append(rows, l) }})
	Logger(ctx).Error("model.finished", "state", "failed", "error_kind", "deadline_exceeded", "api_key", "secret-marker", "error", "secret-marker", "prompt", "secret-marker")
	if len(rows) != 1 || rows[0].Level != "error" || rows[0].Module != "model" || rows[0].SpanID != "generation" || rows[0].Error["error_kind"] != "deadline_exceeded" {
		t.Fatalf("bad structured record %+v", rows)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "secret-marker") {
		t.Fatal("captured raw content")
	}
	Logger(context.Background()).Info("http.request")
	if len(rows) != 1 {
		t.Fatal("unowned logs persisted")
	}
	_, end := Start(ctx, "sandbox.ensure")
	end(context.DeadlineExceeded)
	if rows[len(rows)-1].Level != "error" {
		t.Fatal("failed span logged as info")
	}
}
