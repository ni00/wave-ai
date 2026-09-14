package bench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/platform/xid"
)

type liveCase struct {
	Name    string                         `json:"name"`
	Session execution.CreateSessionRequest `json:"session"`
	Task    execution.TaskRequest          `json:"task"`
	Expect  expectations                   `json:"expect"`
}
type expectations struct {
	ResultContains string            `json:"result_contains"`
	ResultEquals   *string           `json:"result_equals"`
	Files          []fileExpectation `json:"files"`
}
type fileExpectation struct {
	Path       string          `json:"path"`
	SHA256     string          `json:"sha256"`
	JSONEquals json.RawMessage `json:"json_equals"`
}
type liveReport struct {
	report
	Warmup       []taskSample `json:"warmup_tasks,omitempty"`
	CaseName     string       `json:"case_name"`
	AgentID      string       `json:"agent_id"`
	AgentVersion int          `json:"agent_version"`
	Model        string       `json:"model"`
}

func serviceClient(base, keyFile string) (apiClient, func(), error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return apiClient{}, nil, errors.New("URL must be an HTTP(S) service URL without credentials, query or fragment")
	}
	key := os.Getenv("WAVE_API_KEY")
	if keyFile != "" {
		b, e := os.ReadFile(keyFile)
		if e != nil {
			return apiClient{}, nil, e
		}
		key = strings.TrimSpace(string(b))
	}
	if key == "" {
		return apiClient{}, nil, errors.New("set WAVE_API_KEY or use -key-file (Wave API key, not model provider key)")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 256
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return apiClient{base: strings.TrimRight(base, "/"), key: key, http: client}, transport.CloseIdleConnections, nil
}

func readCase(name string) (liveCase, error) {
	var c liveCase
	f, e := os.Open(name)
	if e != nil {
		return c, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return c, e
	}
	if len(b) > 1<<20 {
		return c, errors.New("case exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("case must contain one JSON object")
	}
	if c.Name == "" || c.Task.AgentID == "" || c.Task.Input == "" {
		return c, errors.New("case requires name, task.agent_id and task.input")
	}
	if c.Expect.ResultContains == "" && c.Expect.ResultEquals == nil && len(c.Expect.Files) == 0 {
		return c, errors.New("case requires at least one result or artifact assertion")
	}
	for _, f := range c.Expect.Files {
		if f.Path == "" || (f.SHA256 == "" && len(f.JSONEquals) == 0) {
			return c, errors.New("file assertions require path and sha256 or json_equals")
		}
		if f.SHA256 != "" {
			b, e := hex.DecodeString(f.SHA256)
			if e != nil || len(b) != 32 {
				return c, errors.New("sha256 must contain 64 hex characters")
			}
		}
	}
	return c, nil
}

func runLive(ctx context.Context, args []string, out, diag io.Writer) error {
	fs := flag.NewFlagSet("bench live", flag.ContinueOnError)
	fs.SetOutput(diag)
	caseFile := fs.String("case", "", "JSON scenario with session, task and expect assertions")
	base := fs.String("url", "http://127.0.0.1:8080", "Wave API URL")
	keyFile := fs.String("key-file", "", "read Wave API key from file; otherwise WAVE_API_KEY")
	o := options{}
	fs.IntVar(&o.Tasks, "tasks", 5, "measured tasks (creates sessions and spends real model tokens)")
	fs.IntVar(&o.Clients, "clients", 1, "concurrent tasks, each in a fresh session")
	fs.IntVar(&o.Warmup, "warmup", 0, "unmeasured tasks; also spend model tokens")
	fs.DurationVar(&o.Poll, "poll", 200*time.Millisecond, "status polling interval")
	fs.DurationVar(&o.Timeout, "timeout", 5*time.Minute, "deadline for each warmup/measured phase")
	fs.StringVar(&o.Format, "format", "json", "json or human")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if fs.NArg() != 0 || *caseFile == "" || o.Tasks < 1 || o.Tasks > 10000 || o.Clients < 1 || o.Clients > 256 || o.Warmup < 0 || o.Warmup > 10000 || o.Poll <= 0 || o.Timeout <= 0 || (o.Format != "json" && o.Format != "human") {
		return errors.New("bench live: require -case, tasks 1..10000, clients 1..256, warmup 0..10000, positive durations and json|human format")
	}
	fixture, e := readCase(*caseFile)
	if e != nil {
		return e
	}
	c, closeClient, e := serviceClient(*base, *keyFile)
	if e != nil {
		return e
	}
	defer closeClient()
	var agent agents.Agent
	if e = c.call(ctx, "GET", "/v1/agents/"+url.PathEscape(fixture.Task.AgentID), nil, &agent); e != nil {
		return e
	}
	r := liveReport{report: report{Version: 2, RunID: xid.New("bench"), Kind: "live", Revision: buildRevision(), Workload: fingerprint(fixture), StartedAt: time.Now().UTC(), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), Options: o, Phases: []phaseReport{}}, CaseName: fixture.Name, AgentID: agent.ID, AgentVersion: agent.Version, Model: agent.Config.Model}
	fmt.Fprintf(diag, "bench live: run=%s case=%s model=%s tasks=%d clients=%d warmup=%d\n", r.RunID, r.CaseName, r.Model, o.Tasks, o.Clients, o.Warmup)
	if o.Warmup > 0 {
		warm, _ := liveLoad(ctx, c, fixture, o.Warmup, o)
		failed := len(warm) != o.Warmup
		for _, sample := range warm {
			r.Warmup = append(r.Warmup, traceData(sample))
			failed = failed || sample.err != nil || sample.traceError || (sample.trace != nil && (sample.trace.Incomplete || sample.trace.Truncated))
		}
		if failed {
			err := errors.New("live warmup failed or incomplete; measured phase not started")
			r.Phases = append(r.Phases, phaseReport{Requested: o.Tasks, PhaseError: err.Error(), States: map[string]int{}})
			if writeErr := writeLiveReport(out, r); writeErr != nil {
				return writeErr
			}
			return err
		}
	}
	samples, elapsed := liveLoad(ctx, c, fixture, o.Tasks, o)
	p := phaseReport{Requested: o.Tasks, States: map[string]int{}}
	p.summarize(samples, elapsed)
	var runErr error
	if p.Failed > 0 || p.Attempted != p.Requested || p.TraceFailures > 0 || p.IncompleteTraces > 0 {
		runErr = errors.New("live benchmark failed, incomplete, or missing complete telemetry; inspect report")
		p.PhaseError = runErr.Error()
	}
	r.Phases = append(r.Phases, p)
	if err := writeLiveReport(out, r); err != nil {
		return err
	}
	return runErr
}

func writeLiveReport(out io.Writer, r liveReport) error {
	p := r.Phases[len(r.Phases)-1]
	if r.Options.Format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if e := enc.Encode(r); e != nil {
			return e
		}
	} else {
		fmt.Fprintf(out, "Wave live benchmark %s | %s | model=%s\n", r.RunID, r.CaseName, r.Model)
		fmt.Fprintf(out, "Validated %d/%d; failed=%d; throughput=%.2f tasks/s; p50=%.1fms p95=%.1fms p99=%.1fms; model-p95=%.1fms; tool-p95=%.1fms\n", p.Succeeded, p.Requested, p.Failed, p.TasksPerSecond, p.EndToEnd.P50, p.EndToEnd.P95, p.EndToEnd.P99, p.Model.P95, p.Tool.P95)
		for _, s := range p.Tasks {
			fmt.Fprintf(out, "  %s %s trace=/v1/tasks/%s/trace cancel_requested=%t cancel_error=%t\n", s.TaskID, s.State, s.TaskID, s.CancelRequested, s.CancelError)
		}
	}
	return nil
}

func liveLoad(ctx context.Context, c apiClient, f liveCase, count int, o options) ([]sample, time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	start := time.Now()
	var next atomic.Int64
	var wg sync.WaitGroup
	ch := make(chan sample, min(o.Clients, count))
	for range min(o.Clients, count) {
		wg.Go(func() {
			for ctx.Err() == nil {
				if next.Add(1) > int64(count) {
					return
				}
				ch <- c.liveTask(ctx, f, o)
			}
		})
	}
	go func() { wg.Wait(); close(ch) }()
	ss := make([]sample, 0, count)
	for s := range ch {
		ss = append(ss, s)
	}
	return ss, time.Since(start)
}

func (c apiClient) liveTask(ctx context.Context, f liveCase, o options) (s sample) {
	start := time.Now()
	defer func() {
		if s.latency == 0 {
			s.latency = time.Since(start)
		}
		if s.err != nil && s.state == "" {
			s.state = "client_error"
			if errors.Is(s.err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.state = "timeout"
			}
			if errors.Is(ctx.Err(), context.Canceled) {
				s.state = "interrupted"
			}
		}
		if s.task.ID != "" && !execution.Terminal(s.task.State) {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			s.cancelRequested = true
			s.cancelError = c.call(cleanup, "POST", "/v1/tasks/"+s.task.ID+"/cancel", nil, nil) != nil
			cancel()
		}
		c.trace(ctx, &s)
		s.task.Result = ""
		s.task.Error = ""
		s.task.Snapshot = agents.Config{}
		s.task.Experts = nil
	}()
	var session execution.Session
	if s.err = c.call(ctx, "POST", "/v1/sessions", f.Session, &session); s.err != nil {
		return
	}
	request := f.Task
	request.Budget = request.Budget.Defaults()
	request.Budget.MaxSeconds = min(request.Budget.MaxSeconds, max(1, int(o.Timeout.Seconds())))
	if s.err = c.call(ctx, "POST", "/v1/sessions/"+session.ID+"/tasks", request, &s.task); s.err != nil {
		return
	}
	for {
		if s.err = c.call(ctx, "GET", "/v1/tasks/"+s.task.ID, nil, &s.task); s.err != nil {
			return
		}
		if execution.Terminal(s.task.State) || s.task.State == "waiting" || s.task.State == "unknown" {
			s.latency = time.Since(start)
			s.state = s.task.State
			if s.state != "succeeded" {
				s.err = fmt.Errorf("task %s: state=%s", s.task.ID, s.state)
				return
			}
			if s.task.StartedAt == nil || s.task.FinishedAt == nil || s.task.CreatedAt.IsZero() {
				s.err = errors.New("completed task missing timestamps")
			} else {
				s.err = c.validate(ctx, s.task, f.Expect)
			}
			if s.err != nil {
				s.state = "invalid_result"
			}
			return
		}
		if s.err = pause(ctx, o.Poll); s.err != nil {
			return
		}
	}
}

func (c apiClient) validate(ctx context.Context, t execution.Task, e expectations) error {
	if e.ResultContains != "" && !strings.Contains(t.Result, e.ResultContains) {
		return errors.New("result_contains assertion failed")
	}
	if e.ResultEquals != nil && t.Result != *e.ResultEquals {
		return errors.New("result_equals assertion failed")
	}
	if len(e.Files) == 0 {
		return nil
	}
	var found []files.File
	for offset := 0; ; {
		var page files.ListResponse
		if err := c.call(ctx, "GET", fmt.Sprintf("/v1/files?task_id=%s&limit=200&offset=%d", url.QueryEscape(t.ID), offset), nil, &page); err != nil {
			return err
		}
		found = append(found, page.Data...)
		if page.NextOffset == 0 {
			break
		}
		if page.NextOffset <= offset || len(found) > 10000 {
			return errors.New("artifact listing exceeded limit")
		}
		offset = page.NextOffset
	}
	for _, expected := range e.Files {
		var file *files.File
		for i := range found {
			if found[i].Path == expected.Path {
				file = &found[i]
				break
			}
		}
		if file == nil {
			return errors.New("required artifact missing")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/files/"+url.PathEscape(file.ID)+"/content", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		res, err := c.http.Do(req)
		if err != nil {
			return errors.New("artifact download failed")
		}
		b, readErr := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
		res.Body.Close()
		if res.StatusCode != 200 || readErr != nil || len(b) > 2<<20 {
			return errors.New("artifact download failed or exceeds 2 MiB assertion limit")
		}
		if expected.SHA256 != "" && !strings.EqualFold(expected.SHA256, fmt.Sprintf("%x", sha256.Sum256(b))) {
			return errors.New("artifact sha256 assertion failed")
		}
		if len(expected.JSONEquals) > 0 {
			if !equalJSON(b, expected.JSONEquals) {
				return errors.New("artifact json_equals assertion failed")
			}
		}
	}
	return nil
}
