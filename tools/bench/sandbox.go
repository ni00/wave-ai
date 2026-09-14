package bench

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	common "github.com/docker/sandboxes-api/gen/go/docker/sbx/common/v1"
	v1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/v1"
	sdk "github.com/docker/sandboxes-api/gen/go/sbx"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

type sandboxOptions struct {
	Backend     string        `json:"backend"`
	Verify      bool          `json:"verify"`
	Concurrency string        `json:"concurrency"`
	Memory      string        `json:"memory_mib"`
	CPUs        int           `json:"cpus"`
	Iterations  int           `json:"iterations_per_client"`
	Command     string        `json:"command"`
	Image       string        `json:"image"`
	Timeout     time.Duration `json:"sample_timeout_ns"`
	Format      string        `json:"-"`
}

type sandboxSample struct {
	Checks       []string `json:"checks,omitempty"`
	Name         string   `json:"sandbox_name"`
	Concurrency  int      `json:"concurrency"`
	MemoryMiB    int      `json:"memory_mib"`
	CreateMS     float64  `json:"create_ms"`
	CommandMS    float64  `json:"command_ms"`
	StopMS       float64  `json:"stop_ms"`
	RestartMS    float64  `json:"restart_ms"`
	Output       string   `json:"output,omitempty"`
	Error        string   `json:"error,omitempty"`
	CleanupError string   `json:"cleanup_error,omitempty"`
}

// runSandbox exercises real microVMs using the same adapter as Wave. It does
// not use a model, service DB, user sessions, or the normal worker benchmark.
func runSandbox(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	var o sandboxOptions
	fs := flag.NewFlagSet("bench sandbox", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	fs.StringVar(&o.Backend, "backend", "gvisor", "gvisor, podman or sbx")
	fs.BoolVar(&o.Verify, "verify", false, "verify container file isolation, idempotency, bounded output and timeout cleanup")
	fs.StringVar(&o.Concurrency, "concurrency", "1,2", "concurrent sandbox clients; independent of service capacity limits")
	fs.StringVar(&o.Memory, "memory-mib", "512,1024,2048", "guest memory sizes to compare")
	fs.IntVar(&o.CPUs, "cpus", 1, "guest vCPUs")
	fs.IntVar(&o.Iterations, "iterations", 3, "new sandboxes per client and memory size")
	fs.StringVar(&o.Command, "command", "printf 'wave sandbox benchmark\\n'; head -c 1048576 /dev/zero > /workspace/bench-data; sha256sum /workspace/bench-data", "bash workload inside each sandbox; use a representative script to test memory/CPU pressure")
	fs.StringVar(&o.Image, "image", "", "sandbox image; defaults to backend image environment variable")
	fs.DurationVar(&o.Timeout, "timeout", 3*time.Minute, "deadline per sample, excluding forced cleanup")
	fs.StringVar(&o.Format, "format", "human", "human or json")
	fs.Usage = func() {
		fmt.Fprintln(diagnostics, "Usage: wave bench sandbox [flags]\nRequires Docker for disposable PostgreSQL and the selected sandbox engine.\nReads WAVE_SBX_*, WAVE_DOCKER_HOST, WAVE_PODMAN_HOST, WAVE_CONTAINER_IMAGE/NETWORK and WAVE_GVISOR_RUNTIME. Deletes only its own sandbox disks.\nUse a dedicated test host: benchmark capacity is independent of running Wave deployments.\nMeasures create, command, stop and restart times; does not measure host RSS or peak guest memory.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench sandbox: unexpected arguments %v", fs.Args())
	}
	counts, err := sandboxIntegers(o.Concurrency, 1, 64)
	if err != nil {
		return fmt.Errorf("concurrency: %w", err)
	}
	memories, err := sandboxIntegers(o.Memory, 512, 32768)
	if err != nil {
		return fmt.Errorf("memory-mib: %w", err)
	}
	if o.CPUs < 1 || o.CPUs > 16 || o.Iterations < 1 || o.Iterations > 100 || o.Timeout <= 0 || strings.TrimSpace(o.Command) == "" {
		return errors.New("bench sandbox: invalid CPUs, iterations, timeout or command")
	}
	if o.Format != "human" && o.Format != "json" {
		return errors.New("format must be human or json")
	}
	if o.Backend != "sbx" && o.Backend != "gvisor" && o.Backend != "podman" {
		return errors.New("backend must be sbx, gvisor or podman")
	}
	if o.Verify && o.Backend == "sbx" {
		return errors.New("container isolation verification requires gvisor or podman")
	}
	if o.Image == "" {
		if o.Backend == "sbx" {
			o.Image = os.Getenv("WAVE_SBX_IMAGE")
			if o.Image == "" {
				o.Image = "docker.io/docker/sandbox-ubuntu-24.04:latest"
			}
		} else {
			o.Image = os.Getenv("WAVE_CONTAINER_IMAGE")
			if o.Image == "" {
				o.Image = sandbox.DefaultContainerImage
			}
		}
	}
	baseURL := os.Getenv("WAVE_SBX_URL")
	if baseURL == "" {
		baseURL = "http://localhost:6060"
	}
	token := os.Getenv("WAVE_SBX_TOKEN")
	var client *sdk.Client
	if o.Backend == "sbx" {
		client = sdk.New(&http.Client{Timeout: 45 * time.Second, Transport: sandboxTransport{token}}, baseURL)
		if _, err := client.Capabilities().GetCapabilities(ctx, connect.NewRequest(&v1.GetCapabilitiesRequest{})); err != nil {
			return fmt.Errorf("bench sandbox: sbx API unavailable: %w", err)
		}
	}
	pg, cleanup, err := startPostgres(ctx, diagnostics)
	if err != nil {
		return err
	}
	defer cleanup()
	dsn, err := pg.database(ctx, 0)
	if err != nil {
		return err
	}
	db, err := database.Open(ctx, dsn)
	if err != nil {
		return err
	}
	pool, _ := db.DB()
	defer pool.Close()
	if err := db.AutoMigrate(&sandbox.Record{}, &sandbox.Process{}); err != nil {
		return err
	}
	runID := xid.New("bench")
	parent := "wave"
	samples := []sandboxSample{}
	var runErr error
	for _, memory := range memories {
		for _, concurrency := range counts {
			if ctx.Err() != nil {
				runErr = errors.Join(runErr, ctx.Err())
				break
			}
			fmt.Fprintf(diagnostics, "bench sandbox: %d MiB, concurrency=%d, iterations=%d\n", memory, concurrency, o.Iterations)
			var box sandbox.Provider
			if o.Backend == "sbx" {
				box = sandbox.NewSbx(sandbox.SbxOptions{BaseURL: baseURL, Token: token, Parent: parent, Image: o.Image, CPUs: uint32(o.CPUs), MemoryMiB: uint64(memory), MaxRunning: concurrency, MemoryBudgetMiB: uint64(memory * concurrency), Store: db})
			} else {
				host := os.Getenv("WAVE_DOCKER_HOST")
				if o.Backend == "podman" {
					host = os.Getenv("WAVE_PODMAN_HOST")
				}
				box, err = sandbox.NewContainer(sandbox.ContainerOptions{Backend: o.Backend, Host: host, Image: o.Image, Runtime: os.Getenv("WAVE_GVISOR_RUNTIME"), Network: os.Getenv("WAVE_CONTAINER_NETWORK"), CPUs: uint32(o.CPUs), MemoryMiB: uint64(memory), MaxRunning: concurrency, MemoryBudgetMiB: uint64(memory * concurrency), Store: db})
				if err != nil {
					return err
				}
			}
			results := make([]sandboxSample, concurrency*o.Iterations)
			var wg sync.WaitGroup
			for n := range concurrency {
				wg.Go(func() {
					for iteration := range o.Iterations {
						index := n*o.Iterations + iteration
						sid := fmt.Sprintf("%s-%d-%d-%d", runID, memory, concurrency, index)
						results[index] = sandboxTrial(ctx, box, client, db, parent, sid, memory, concurrency, o)
						if results[index].CleanupError != "" || ctx.Err() != nil {
							return
						}
					}
				})
			}
			wg.Wait()
			if closer, ok := box.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			cleanupFailed := false
			for _, sample := range results {
				if sample.Name == "" {
					continue
				}
				samples = append(samples, sample)
				if sample.Error != "" || sample.CleanupError != "" {
					runErr = errors.New("one or more sandbox samples failed; see report")
				}
				if sample.CleanupError != "" {
					cleanupFailed = true
					fmt.Fprintf(diagnostics, "cleanup required for %s: %s\n", sample.Name, sample.CleanupError)
				}
			}
			if cleanupFailed {
				return errors.Join(runErr, writeSandboxReport(out, o, samples))
			}
		}
	}
	return errors.Join(runErr, writeSandboxReport(out, o, samples))
}

func sandboxTrial(ctx context.Context, box sandbox.Provider, client *sdk.Client, db *gorm.DB, parent, sid string, memory, concurrency int, o sandboxOptions) (r sandboxSample) {
	r.Name, r.MemoryMiB, r.Concurrency = "wave-"+sid, memory, concurrency
	run, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		if container, ok := box.(*sandbox.Container); ok {
			err = container.Destroy(cleanup, sid)
		} else {
			err = deleteBenchmarkSandbox(cleanup, client, parent, r.Name)
		}
		if err != nil {
			r.CleanupError = err.Error()
		} else if err := db.WithContext(cleanup).Where("session_id = ?", sid).Delete(&sandbox.Record{}).Error; err != nil {
			r.CleanupError = err.Error()
		}
	}()
	timed := func(dest *float64, fn func() error) bool {
		start := time.Now()
		err := fn()
		*dest = float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			r.Error = err.Error()
			return false
		}
		return true
	}
	if !timed(&r.CreateMS, func() error { return box.Ensure(run, sid) }) {
		return
	}
	if !timed(&r.CommandMS, func() error {
		result, err := box.Exec(run, sid, sandbox.ExecRequest{Command: o.Command, RequestID: sid + "-command"})
		if err != nil {
			return err
		}
		r.Output = result.Stdout + result.Stderr
		if result.ExitCode != 0 {
			return fmt.Errorf("command exited %d", result.ExitCode)
		}
		return box.WriteFile(run, sid, "/workspace/.wave-bench-retained", []byte(sid))
	}) {
		return
	}
	if o.Verify {
		if err := verifyContainer(run, box, sid, &r.Checks); err != nil {
			r.Error = err.Error()
			return
		}
	}
	if !timed(&r.StopMS, func() error { return box.Stop(run, sid) }) {
		return
	}
	timed(&r.RestartMS, func() error {
		if err := box.Ensure(run, sid); err != nil {
			return err
		}
		data, err := box.ReadFile(run, sid, "/workspace/.wave-bench-retained", 1024)
		if err != nil {
			return err
		}
		if string(data) != sid {
			return errors.New("sandbox disk did not survive stop/restart")
		}
		return nil
	})
	return
}

func deleteBenchmarkSandbox(ctx context.Context, client *sdk.Client, parent, name string) error {
	op, err := client.Sandboxes().DeleteSandbox(ctx, connect.NewRequest(&v1.DeleteSandboxRequest{Sandbox: &common.SandboxRef{Parent: parent, Identifier: &common.SandboxRef_Name{Name: name}}, Force: true, RequestId: "delete-" + name}))
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	for {
		result, err := client.Operations().WaitOperation(ctx, connect.NewRequest(&v1.WaitOperationRequest{Id: op.Msg.Id, Timeout: durationpb.New(10 * time.Second)}))
		if err != nil {
			return err
		}
		if result.Msg.Done {
			if result.Msg.GetError() != nil {
				return fmt.Errorf("delete failed: %s", result.Msg.GetError())
			}
			return nil
		}
		if err := pause(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

type sandboxTransport struct{ token string }

func (t sandboxTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func sandboxIntegers(raw string, low, high int) ([]int, error) {
	values := []int{}
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < low || n > high || seen[n] {
			return nil, fmt.Errorf("expected unique integers from %d to %d", low, high)
		}
		seen[n] = true
		values = append(values, n)
	}
	return values, nil
}

func writeSandboxReport(out io.Writer, o sandboxOptions, samples []sandboxSample) error {
	if o.Format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Version int             `json:"version"`
			Options sandboxOptions  `json:"options"`
			Samples []sandboxSample `json:"samples"`
		}{1, o, samples})
	}
	var b strings.Builder
	fmt.Fprintln(&b, "Real sandbox benchmark; milliseconds. Default workload tests lifecycle and small file I/O, not memory capacity.")
	fmt.Fprintln(&b, "MiB  concurrent  create-ms  command-ms  stop-ms  restart-ms  outcome")
	for _, r := range samples {
		status := "ok"
		if r.Error != "" || r.CleanupError != "" {
			status = r.Error + " " + r.CleanupError
		}
		fmt.Fprintf(&b, "%4d %11d %10.1f %11.1f %8.1f %11.1f %s\n", r.MemoryMiB, r.Concurrency, r.CreateMS, r.CommandMS, r.StopMS, r.RestartMS, status)
	}
	fmt.Fprintln(&b, "Image cache is shared: create is a fresh sandbox, not necessarily a cold image pull. Host RSS/peak memory are not measured. Use JSON for sandbox names and workload output.")
	_, err := io.WriteString(out, b.String())
	return err
}
