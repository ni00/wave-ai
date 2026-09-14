package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"wave-ai.local/wave/internal/app"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/config"
)

type apiClient struct {
	base, key string
	http      *http.Client
}

func (c apiClient) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d", method, path, res.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, res.Body)
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(out)
}

func runPhase(ctx context.Context, dsn, dir string, workers int, o options) (phaseReport, error) {
	p := phaseReport{Workers: workers, Requested: o.Tasks, States: map[string]int{}}
	setup, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	defer setupCancel()
	if err := app.Init(setup, dsn); err != nil {
		return p, err
	}
	model := httptest.NewServer(modelHandler(o))
	defer model.Close()
	cfg := &config.Config{
		Role: "worker", DatabaseURL: dsn, DataDir: dir,
		StorageBackend: "local", SandboxBackend: "sbx",
		WorkerConcurrency: workers, LeaseSeconds: 30, ContextTokens: 32000,
		ModelBaseURL: model.URL, ModelTimeoutSec: max(1, int(o.Timeout.Seconds())+1),
	}
	a, err := app.New(setup, cfg)
	if err != nil {
		return p, err
	}
	defer a.Close()
	key, err := auth.Bootstrap(setup, a.DB, "bench", "bench", "bench", auth.ScopeAPI)
	if err != nil {
		return p, err
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = o.Clients
	transport.MaxIdleConns = o.Clients * 2
	defer transport.CloseIdleConnections()
	c := apiClient{base: server.URL, key: key, http: &http.Client{Transport: transport, Timeout: min(o.Timeout, 30*time.Second)}}
	var agent agents.Agent
	if err := c.call(setup, "POST", "/v1/agents", agents.Config{Name: "benchmark", Model: "synthetic", Instructions: "Return the synthetic response."}, &agent); err != nil {
		return p, err
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()
	defer func() { stop(); <-done }()
	if o.Warmup > 0 {
		warm, _ := load(runCtx, c, agent.ID, o.Warmup, o)
		for _, s := range warm {
			if s.err != nil {
				return p, fmt.Errorf("bench warmup: %w", s.err)
			}
		}
		if len(warm) != o.Warmup {
			return p, errors.New("bench: warmup incomplete")
		}
	}
	// Pool waits exclude initialization and warmup. Heap includes the entire Go process.
	pool, _ := a.DB.DB()
	before := pool.Stats()
	runtime.GC()
	stopSampling := sampleHeap()
	samples, elapsed := load(runCtx, c, agent.ID, o.Tasks, o)
	p.PeakHeapMiB = float64(stopSampling()) / (1 << 20)
	after := pool.Stats()
	p.DBWaitCount = after.WaitCount - before.WaitCount
	p.DBWaitMS = float64(after.WaitDuration-before.WaitDuration) / float64(time.Millisecond)
	p.summarize(samples, elapsed)
	if p.Failed > 0 || p.Attempted != p.Requested || p.TraceFailures > 0 || p.IncompleteTraces > 0 {
		return p, fmt.Errorf("bench: workers=%d had %d failed and %d unattempted tasks", workers, p.Failed, p.Requested-p.Attempted)
	}
	return p, ctx.Err()
}

type sample struct {
	task            execution.Task
	latency         time.Duration
	err             error
	state           string
	trace           *execution.Trace
	traceError      bool
	cancelRequested bool
	cancelError     bool
}

// load is a closed-loop test: a client starts a new session/task only after its
// previous task completes. One session per task avoids session serialization.
func load(ctx context.Context, c apiClient, agent string, count int, o options) ([]sample, time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	var next atomic.Int64
	var wg sync.WaitGroup
	results := make(chan sample, min(o.Clients, count))
	start := time.Now()
	for range min(o.Clients, count) {
		wg.Go(func() {
			for ctx.Err() == nil {
				if next.Add(1) > int64(count) {
					return
				}
				results <- c.task(ctx, agent, o)
			}
		})
	}
	go func() { wg.Wait(); close(results) }()
	samples := make([]sample, 0, count)
	for s := range results {
		samples = append(samples, s)
	}
	return samples, time.Since(start)
}

func (c apiClient) task(ctx context.Context, agent string, o options) (s sample) {
	start := time.Now()
	defer func() {
		s.latency = time.Since(start)
		c.trace(ctx, &s)
		// Keep only timing/outcome data; do not retain response payloads or
		// snapshots for every completed task in the load generator's heap.
		s.task.Result = ""
		s.task.Snapshot = agents.Config{}
		s.task.Experts = nil
		if s.err != nil && s.state == "" {
			s.state = "client_error"
			if errors.Is(s.err, context.DeadlineExceeded) {
				s.state = "timeout"
			}
			if errors.Is(s.err, context.Canceled) {
				s.state = "interrupted"
			}
		}
	}()
	var session execution.Session
	if s.err = c.call(ctx, "POST", "/v1/sessions", execution.CreateSessionRequest{Title: "benchmark"}, &session); s.err != nil {
		return
	}
	if s.err = c.call(ctx, "POST", "/v1/sessions/"+session.ID+"/tasks", execution.TaskRequest{AgentID: agent, Input: "benchmark", Budget: execution.Budget{MaxSeconds: max(1, int(o.Timeout.Seconds())+1)}}, &s.task); s.err != nil {
		return
	}
	path := "/v1/tasks/" + s.task.ID
	for {
		if s.err = c.call(ctx, "GET", path, nil, &s.task); s.err != nil {
			return
		}
		if execution.Terminal(s.task.State) || s.task.State == "unknown" || s.task.State == "waiting" {
			s.state = s.task.State
			if s.state != "succeeded" {
				s.err = fmt.Errorf("task %s: state=%s", s.task.ID, s.state)
			} else if s.task.StartedAt == nil || s.task.FinishedAt == nil || s.task.CreatedAt.IsZero() {
				s.state = "invalid_result"
				s.err = errors.New("bench: completed task missing timestamps")
			} else if s.task.Result != expectedResponse(o) {
				s.state = "invalid_result"
				s.err = errors.New("bench: completed task response differs from synthetic model")
			}
			return
		}
		if s.err = pause(ctx, o.Poll); s.err != nil {
			return
		}
	}
}

func sampleHeap() func() uint64 {
	stop, done := make(chan struct{}), make(chan uint64, 1)
	go func() {
		var peak uint64
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		read := func() { var m runtime.MemStats; runtime.ReadMemStats(&m); peak = max(peak, m.HeapAlloc) }
		read()
		for {
			select {
			case <-ticker.C:
				read()
			case <-stop:
				read()
				done <- peak
				return
			}
		}
	}()
	return func() uint64 { close(stop); return <-done }
}
