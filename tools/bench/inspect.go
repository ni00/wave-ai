package bench

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"

	"wave-ai.local/wave/internal/modules/execution"
)

// Inspect exports a native task trace without depending on a telemetry backend.
func Inspect(ctx context.Context, args []string, out, diag io.Writer) error {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	fs.SetOutput(diag)
	id := fs.String("task", "", "task ID")
	base := fs.String("url", "http://127.0.0.1:8080", "Wave API URL")
	keyFile := fs.String("key-file", "", "Wave API key file; otherwise WAVE_API_KEY")
	format := fs.String("format", "human", "human, json or chrome (Chrome/Perfetto timeline JSON)")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if fs.NArg() != 0 || *id == "" || (*format != "human" && *format != "json" && *format != "chrome") {
		return errors.New("trace requires -task and human|json|chrome format")
	}
	c, closeClient, e := serviceClient(*base, *keyFile)
	if e != nil {
		return e
	}
	defer closeClient()
	var tr execution.Trace
	if e = c.call(ctx, "GET", "/v1/tasks/"+url.PathEscape(*id)+"/trace", nil, &tr); e != nil {
		return e
	}
	if tr.Version != 1 || tr.TaskID != *id {
		return errors.New("invalid trace response")
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	switch *format {
	case "json":
		return enc.Encode(tr)
	case "chrome":
		events := []map[string]any{{"ph": "M", "name": "process_name", "pid": 1, "tid": 0, "args": map[string]any{"name": tr.TraceID}}}
		lanes := map[string]int{}
		for _, s := range tr.Spans {
			if lanes[s.TaskID] == 0 {
				lanes[s.TaskID] = len(lanes) + 1
				events = append(events, map[string]any{"ph": "M", "name": "thread_name", "pid": 1, "tid": lanes[s.TaskID], "args": map[string]any{"name": s.TaskID}})
			}
			e := map[string]any{"name": s.Name, "cat": s.Kind, "pid": 1, "tid": lanes[s.TaskID], "ts": float64(s.StartedAt.UnixMicro()), "args": map[string]any{"span_id": s.ID, "parent_id": s.ParentID, "state": s.State, "attributes": s.Attributes}}
			if s.DurationMS != nil {
				e["ph"] = "X"
				e["dur"] = *s.DurationMS * 1000
			} else {
				e["ph"] = "i"
				e["s"] = "t"
			}
			events = append(events, e)
		}
		return enc.Encode(map[string]any{"traceEvents": events, "displayTimeUnit": "ms", "wave": map[string]any{"task_id": tr.TaskID, "trace_id": tr.TraceID, "incomplete": tr.Incomplete, "truncated": tr.Truncated}})
	default:
		fmt.Fprintf(out, "Trace %s | task %s | %s | incomplete=%t truncated=%t\n", tr.TraceID, tr.TaskID, tr.State, tr.Incomplete, tr.Truncated)
		fmt.Fprintf(out, "Model %.2fms (%d calls); tools %.2fms (%d calls); tokens input=%d output=%d known=%t\n", tr.Summary.ModelMS, tr.Summary.ModelCalls, tr.Summary.ToolMS, tr.Summary.ToolCalls, tr.Summary.InputTokens, tr.Summary.OutputTokens, tr.Summary.UsageKnown)
		for _, s := range tr.Spans {
			ms := "unknown"
			if s.DurationMS != nil {
				ms = fmt.Sprintf("%.2fms", *s.DurationMS)
			}
			fmt.Fprintf(out, "  %s %-22s %-12s %s\n", s.ID, s.Name, s.State, ms)
		}
		return nil
	}
}
