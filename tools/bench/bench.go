// Package bench runs an isolated, synthetic end-to-end Wave benchmark.
package bench

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type options struct {
	Workers    string        `json:"workers"`
	Tasks      int           `json:"tasks_per_phase"`
	Clients    int           `json:"clients"`
	Warmup     int           `json:"warmup_tasks_per_phase"`
	ModelDelay time.Duration `json:"model_delay_ns"`
	Chunks     int           `json:"model_chunks"`
	ChunkBytes int           `json:"chunk_bytes"`
	Poll       time.Duration `json:"poll_interval_ns"`
	Timeout    time.Duration `json:"phase_timeout_ns"`
	Format     string        `json:"-"`
}

type report struct {
	Version    int           `json:"version"`
	StartedAt  time.Time     `json:"started_at"`
	GoVersion  string        `json:"go_version"`
	OS         string        `json:"os"`
	Arch       string        `json:"arch"`
	CPUs       int           `json:"logical_cpus"`
	GOMAXPROCS int           `json:"gomaxprocs"`
	Options    options       `json:"options"`
	Phases     []phaseReport `json:"phases"`
}

// Run does not load service environment configuration or use provider keys.
// All state belongs to a disposable PostgreSQL container and temporary directory.
func Run(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	var o options
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	fs.StringVar(&o.Workers, "workers", "2,5,10,20", "comma-separated worker counts; each runs in a fresh database")
	fs.IntVar(&o.Tasks, "tasks", 100, "measured tasks per worker count")
	fs.IntVar(&o.Clients, "clients", 32, "maximum in-flight tasks (closed-loop load)")
	fs.IntVar(&o.Warmup, "warmup", 10, "warmup tasks per worker count, excluded from results")
	fs.DurationVar(&o.ModelDelay, "model-delay", 100*time.Millisecond, "total simulated model streaming delay per task")
	fs.IntVar(&o.Chunks, "chunks", 8, "streaming response chunks per task")
	fs.IntVar(&o.ChunkBytes, "chunk-bytes", 128, "ASCII bytes per response chunk")
	fs.DurationVar(&o.Poll, "poll", 100*time.Millisecond, "task status polling interval; contributes API load and observed latency")
	fs.DurationVar(&o.Timeout, "timeout", 2*time.Minute, "deadline for each warmup or measured phase")
	fs.StringVar(&o.Format, "format", "human", "human or json")
	fs.Usage = func() {
		fmt.Fprintln(diagnostics, "Usage: wave bench [flags]\nRequires a local Docker daemon; creates and removes a temporary PostgreSQL 16 container.\nExercises HTTP authentication, task admission, workers, model streaming and persistence.\nUses a local synthetic model; no provider credentials, sandboxes or S3 workload.\nService WAVE_* configuration is ignored. stdout is the report; progress goes to stderr.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench: unexpected arguments %v", fs.Args())
	}
	workers, err := parseWorkers(o.Workers)
	if err != nil {
		return err
	}
	if o.Tasks < 1 || o.Clients < 1 || o.Warmup < 0 || o.Chunks < 1 || o.ChunkBytes < 1 || o.ModelDelay < 0 || o.Poll <= 0 || o.Timeout <= 0 {
		return errors.New("bench: tasks, clients, chunks, chunk-bytes, poll and timeout must be positive; warmup and model-delay must be nonnegative")
	}
	// Bound the synthetic response to avoid allocating an accidental multi-GB payload.
	if o.Chunks > 4096 || o.ChunkBytes > (1<<20)/o.Chunks {
		return errors.New("bench: at most 4096 chunks and 1 MiB total response are supported")
	}
	if o.Format != "human" && o.Format != "json" {
		return errors.New("bench: format must be human or json")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "wave-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, cleanup, err := startPostgres(ctx, diagnostics)
	if err != nil {
		return err
	}
	defer cleanup()
	r := report{Version: 1, StartedAt: time.Now().UTC(), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), Options: o, Phases: []phaseReport{}}
	var runErr error
	for i, n := range workers {
		fmt.Fprintf(diagnostics, "bench: workers=%d, clients=%d, warmup=%d, tasks=%d\n", n, min(o.Clients, o.Tasks), o.Warmup, o.Tasks)
		dsn, err := db.database(ctx, i)
		if err != nil {
			runErr = err
			break
		}
		p, err := runPhase(ctx, dsn, dir, n, o)
		if err != nil {
			p.PhaseError = err.Error()
		}
		r.Phases = append(r.Phases, p)
		fmt.Fprintf(diagnostics, "bench: workers=%d succeeded=%d/%d tasks/s=%.2f p95=%.1fms\n", n, p.Succeeded, p.Attempted, p.TasksPerSecond, p.EndToEnd.P95)
		if err != nil {
			runErr = err
			break
		}
	}
	if err := writeReport(out, r); err != nil {
		return err
	}
	return runErr
}

func parseWorkers(raw string) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for _, s := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 || n > 1024 || seen[n] {
			return nil, errors.New("bench: workers must be unique integers between 1 and 1024")
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

func writeReport(out io.Writer, r report) error {
	if r.Options.Format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Wave synthetic benchmark | %s %s/%s | CPUs=%d GOMAXPROCS=%d\n", r.GoVersion, r.OS, r.Arch, r.CPUs, r.GOMAXPROCS)
	fmt.Fprintf(&b, "Model delay=%s, chunks=%d x %d bytes, clients=%d, poll=%s\n", r.Options.ModelDelay, r.Options.Chunks, r.Options.ChunkBytes, r.Options.Clients, r.Options.Poll)
	fmt.Fprintln(&b, "workers  ok/attempted  errors  tasks/s  e2e-p50  e2e-p95  e2e-p99  queue-p95  exec-p95  heap-MiB")
	for _, p := range r.Phases {
		fmt.Fprintf(&b, "%7d  %4d/%-8d %6d  %7.2f  %7.1f  %7.1f  %7.1f  %9.1f  %8.1f  %8.1f\n", p.Workers, p.Succeeded, p.Attempted, p.Failed, p.TasksPerSecond, p.EndToEnd.P50, p.EndToEnd.P95, p.EndToEnd.P99, p.Queue.P95, p.Execution.P95, p.PeakHeapMiB)
		for _, e := range p.Errors {
			fmt.Fprintf(&b, "  %s\n", e)
		}
		if p.PhaseError != "" {
			fmt.Fprintf(&b, "  phase: %s\n", p.PhaseError)
		}
	}
	fmt.Fprintln(&b, "Latencies are milliseconds, successful tasks only; e2e includes session creation and polling.\nHeap is sampled for the whole Go process (Wave + load generator + mock), excludes PostgreSQL and is not RSS.\nSynthetic closed-loop baseline only: no sandbox, real model, S3/file load, scheduler or SSE subscribers.\nUse -format json for full distributions, failure states, DB pool waits and timings.")
	_, err := io.WriteString(out, b.String())
	return err
}
