package bench

import (
	"context"
	"time"

	"wave-ai.local/wave/internal/modules/execution"
)

type taskSample struct {
	TaskID          string           `json:"task_id"`
	State           string           `json:"state"`
	ObservedMS      float64          `json:"observed_ms"`
	Trace           *execution.Trace `json:"trace,omitempty"`
	TraceError      bool             `json:"trace_error,omitempty"`
	CancelRequested bool             `json:"cancel_requested,omitempty"`
	CancelError     bool             `json:"cancel_error,omitempty"`
}

func (c apiClient) trace(ctx context.Context, s *sample) {
	if s.task.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var tr execution.Trace
	err := c.call(ctx, "GET", "/v1/tasks/"+s.task.ID+"/trace", nil, &tr)
	if err != nil || tr.TaskID != s.task.ID || tr.Version != 1 {
		s.traceError = true
		return
	}
	s.trace = &tr
}

// traceData is kept in the report because synthetic benchmark databases are
// disposable. It contains no response bodies, snapshots or tool commands.
func traceData(s sample) taskSample {
	return taskSample{TaskID: s.task.ID, State: s.state, ObservedMS: float64(s.latency) / float64(time.Millisecond), Trace: s.trace, TraceError: s.traceError, CancelRequested: s.cancelRequested, CancelError: s.cancelError}
}
