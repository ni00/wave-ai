package bench

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"time"

	"wave-ai.local/wave/internal/platform/xid"
)

type postgres struct{ name, address string }

func docker(ctx context.Context, args ...string) (string, error) {
	raw, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bench docker %s: %w: %s", args[0], err, strings.TrimSpace(string(raw)))
	}
	return strings.TrimSpace(string(raw)), nil
}

func startPostgres(ctx context.Context, diagnostics io.Writer) (*postgres, func(), error) {
	p := &postgres{name: xid.New("wave-bench")}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := docker(cleanupCtx, "rm", "-fv", p.name); err != nil {
			fmt.Fprintf(diagnostics, "bench: cleanup failed; remove container %s manually: %v\n", p.name, err)
		}
	}
	fmt.Fprintf(diagnostics, "bench: starting disposable PostgreSQL container %s\n", p.name)
	startup, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, err := docker(startup, "run", "--name", p.name, "-e", "POSTGRES_USER=wave", "-e", "POSTGRES_PASSWORD=wave-bench", "-p", "127.0.0.1::5432", "-d", "postgres:16-alpine")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	address, err := docker(startup, "port", p.name, "5432/tcp")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	address = strings.TrimSpace(strings.Split(address, "\n")[0])
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		cleanup()
		return nil, nil, fmt.Errorf("bench: unexpected PostgreSQL address %q", address)
	}
	p.address = address
	for {
		// TCP avoids the temporary Unix-only server used during image initialization.
		if _, err = docker(startup, "exec", p.name, "pg_isready", "-h", "127.0.0.1", "-U", "wave"); err == nil {
			return p, cleanup, nil
		}
		if err = pause(startup, 250*time.Millisecond); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("bench: PostgreSQL startup: %w", err)
		}
	}
}

func (p *postgres) database(ctx context.Context, index int) (string, error) {
	name := fmt.Sprintf("bench_%d", index)
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := docker(commandCtx, "exec", p.name, "createdb", "-U", "wave", name); err != nil {
		return "", err
	}
	return "postgres://wave:wave-bench@" + p.address + "/" + name + "?sslmode=disable", nil
}

func pause(ctx context.Context, duration time.Duration) error {
	t := time.NewTimer(duration)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
