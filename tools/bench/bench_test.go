package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/execution"
)

func TestValidationBeforeDocker(t *testing.T) {
	for _, args := range [][]string{
		{"-workers", "0"}, {"-workers", "2,2"}, {"-workers", "1,"},
		{"-tasks", "0"}, {"-clients", "0"}, {"-warmup", "-1"},
		{"-chunks", "0"}, {"-chunks", "4097"}, {"-chunk-bytes", "1048577"},
		{"-model-delay", "-1s"}, {"-poll", "0s"}, {"-timeout", "0s"},
		{"-format", "csv"}, {"extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diag bytes.Buffer
			err := Run(context.Background(), args, &out, &diag)
			if err == nil || strings.Contains(diag.String(), "PostgreSQL") {
				t.Fatalf("err=%v diag=%s", err, &diag)
			}
		})
	}
}

func TestSuccessfulLatencyAndFailureAccounting(t *testing.T) {
	start := time.Now()
	claimed, finished := start.Add(20*time.Millisecond), start.Add(50*time.Millisecond)
	p := phaseReport{States: map[string]int{}}
	p.summarize([]sample{
		{state: "succeeded", latency: 60 * time.Millisecond, task: execution.Task{CreatedAt: start, StartedAt: &claimed, FinishedAt: &finished}},
		{state: "timeout", err: context.DeadlineExceeded, latency: time.Second},
	}, 2*time.Second)
	if p.Attempted != 2 || p.Succeeded != 1 || p.Failed != 1 || p.ErrorRate != .5 || p.TasksPerSecond != .5 || p.EndToEnd.Count != 1 || p.EndToEnd.P95 != 60 || p.Queue.P95 != 20 || p.Execution.P95 != 30 {
		t.Fatalf("%+v", p)
	}
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(100 - i)
	}
	d := summarize(values)
	if d.P50 != 50 || d.P95 != 95 || d.P99 != 99 || d.Mean != 50.5 || d.Max != 100 {
		t.Fatal(d)
	}
	if summarize(nil).Count != 0 {
		t.Fatal("empty distribution")
	}
}

func TestSyntheticModelUsesRealStreamClient(t *testing.T) {
	o := options{Chunks: 4, ChunkBytes: 32, ModelDelay: 4 * time.Millisecond}
	s := httptest.NewServer(modelHandler(o))
	defer s.Close()
	var streamed strings.Builder
	final, err := modelclient.New(s.URL, "", 5*time.Second).Stream(context.Background(), modelclient.Request{Model: "synthetic"}, func(d modelclient.Delta) { streamed.WriteString(d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != expectedResponse(o) || final.StopReason != "stop" || final.Usage.CompletionTokens == nil || *final.Usage.CompletionTokens != 32 {
		t.Fatalf("stream=%q final=%+v", streamed.String(), final)
	}
}

func TestLoadReportsErrorsAndDeadline(t *testing.T) {
	for _, status := range []string{"failed", "waiting", "unknown", "http_error", "queued"} {
		t.Run(status, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == "http_error" {
					w.WriteHeader(500)
					return
				}
				if r.Method == "POST" {
					json.NewEncoder(w).Encode(map[string]string{"id": "test"})
					return
				}
				json.NewEncoder(w).Encode(map[string]string{"id": "test", "state": status})
			}))
			defer s.Close()
			o := options{Clients: 2, Timeout: 80 * time.Millisecond, Poll: time.Millisecond}
			samples, _ := load(context.Background(), apiClient{base: s.URL, http: s.Client()}, "agent", 4, o)
			if len(samples) == 0 {
				t.Fatal("no samples")
			}
			for _, sample := range samples {
				if sample.err == nil {
					t.Fatal("failure counted as success")
				}
				if status == "queued" && !errors.Is(sample.err, context.DeadlineExceeded) {
					t.Fatal(sample.err)
				}
			}
		})
	}
}

func TestIsolatedBenchmark(t *testing.T) {
	if os.Getenv("WAVE_BENCH_INTEGRATION") != "1" {
		t.Skip("set WAVE_BENCH_INTEGRATION=1 to run disposable Docker benchmark")
	}
	gin.SetMode(gin.ReleaseMode)
	// Poison normal configuration to prove bench never connects to the deployment.
	t.Setenv("WAVE_DATABASE_URL", "not-a-database")
	t.Setenv("WAVE_MODEL_BASE_URL", "https://invalid.example")
	t.Setenv("WAVE_MODEL_API_KEY", "must-not-appear")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var out, diag bytes.Buffer
	err := Run(ctx, []string{"-workers", "1,3", "-tasks", "6", "-warmup", "2", "-clients", "4", "-model-delay", "10ms", "-poll", "10ms", "-format", "json"}, &out, &diag)
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, &diag, &out)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Phases) != 2 {
		t.Fatal(r)
	}
	for _, p := range r.Phases {
		if p.Attempted != 6 || p.Succeeded != 6 || p.Failed != 0 || p.Queue.Count != 6 || p.Execution.Count != 6 || p.TasksPerSecond <= 0 || p.PeakHeapMiB <= 0 {
			t.Fatalf("%+v", p)
		}
	}
	if strings.Contains(out.String()+diag.String(), "must-not-appear") {
		t.Fatal("credential leaked")
	}
	assertContainerRemoved(t, diag.String())

	out.Reset()
	diag.Reset()
	err = Run(ctx, []string{"-workers", "1,2", "-tasks", "10", "-warmup", "0", "-clients", "2", "-model-delay", "5s", "-timeout", "300ms", "-poll", "10ms", "-format", "json"}, &out, &diag)
	if err == nil {
		t.Fatal("timeout returned success")
	}
	r = report{}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("%v: %s", err, &out)
	}
	if len(r.Phases) != 1 || r.Phases[0].Succeeded != 0 || r.Phases[0].Failed == 0 || r.Phases[0].Attempted >= 10 || r.Phases[0].PhaseError == "" {
		t.Fatalf("%+v", r.Phases)
	}
	assertContainerRemoved(t, diag.String())
}

func assertContainerRemoved(t *testing.T, diagnostics string) {
	t.Helper()
	const prefix = "bench: starting disposable PostgreSQL container "
	_, tail, ok := strings.Cut(diagnostics, prefix)
	if !ok {
		t.Fatal("container name missing")
	}
	name := strings.SplitN(tail, "\n", 2)[0]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := docker(ctx, "ps", "-a", "--filter", "name="+name, "--format", "{{.Names}}")
	if err != nil || result != "" {
		t.Fatalf("container cleanup: %q %v", result, err)
	}
}
