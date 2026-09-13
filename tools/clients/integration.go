package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func randomHex(size int) string   { return hex.EncodeToString(randomBytes(size)) }
func randomBytes(size int) []byte { value := make([]byte, size); _, _ = rand.Read(value); return value }

func ready(ctx context.Context, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		ok, err := check(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("service readiness: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (t *tool) containerPort(ctx context.Context, name, port string) (string, error) {
	result, err := t.output(ctx, t.root, nil, "docker", "port", name, port+"/tcp")
	if err != nil {
		return "", err
	}
	_, mapped, err := net.SplitHostPort(strings.Split(result, "\n")[0])
	return mapped, err
}

func (t *tool) integration(ctx context.Context) error {
	work, err := os.MkdirTemp("", "wave-client-integration-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	suffix := randomHex(5)
	pg, s3 := "wave-clients-pg-"+suffix, "wave-clients-s3-"+suffix
	var containers []string
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if len(containers) > 0 {
			args := append([]string{"docker", "rm", "-fv"}, containers...)
			cmd := t.command(cleanup, t.root, nil, args...)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Run(); err != nil {
				fmt.Fprintln(t.diagnostics, "container cleanup failed; remove", strings.Join(containers, " "), ":", err)
			}
		}
	}()
	containers = append(containers, pg)
	if _, err = t.output(ctx, t.root, nil, "docker", "run", "--name", pg, "-e", "POSTGRES_USER=wave", "-e", "POSTGRES_PASSWORD=wave-test", "-e", "POSTGRES_DB=wave_test", "-p", "127.0.0.1::5432", "-d", "postgres:16-alpine"); err != nil {
		return err
	}
	if err = ready(ctx, func(ctx context.Context) (bool, error) {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err := t.output(attempt, t.root, nil, "docker", "exec", pg, "pg_isready", "-U", "wave")
		return err == nil, nil
	}); err != nil {
		return err
	}
	pgport, err := t.containerPort(ctx, pg, "5432")
	if err != nil {
		return err
	}
	containers = append(containers, s3)
	if _, err = t.output(ctx, t.root, nil, "docker", "run", "--name", s3, "-e", "AWS_ACCESS_KEY_ID=wave-test", "-e", "AWS_SECRET_ACCESS_KEY=wave-test-password", "-e", "S3_BUCKET=wave", "-p", "127.0.0.1::8333", "-d", "chrislusf/seaweedfs:4.46", "mini", "-dir=/data", "-ip=127.0.0.1", "-master.telemetry=false", "-admin.ui=false", "-webdav=false", "-s3.port.iceberg=0", "-s3.port.lance=0", "-s3.autoCreateBucket=false"); err != nil {
		return err
	}
	s3port, err := t.containerPort(ctx, s3, "8333")
	if err != nil {
		return err
	}
	endpoint := "http://127.0.0.1:" + s3port
	if err = ready(ctx, func(ctx context.Context) (bool, error) {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err := t.output(attempt, t.root, nil, "curl", "-fsS", "--head", "--max-time", "1", "--aws-sigv4", "aws:amz:us-east-1:s3", "--user", "wave-test:wave-test-password", endpoint+"/wave")
		return err == nil, nil
	}); err != nil {
		return err
	}
	env := map[string]string{
		"WAVE_TEST_DATABASE_URL": "postgres://wave:wave-test@127.0.0.1:" + pgport + "/wave_test?sslmode=disable",
		"WAVE_DATA_DIR":          filepath.Join(work, "data"), "WAVE_STORAGE_BACKEND": "s3", "WAVE_S3_ENDPOINT": endpoint, "WAVE_S3_REGION": "us-east-1", "WAVE_S3_BUCKET": "wave", "WAVE_S3_PATH_STYLE": "true", "WAVE_S3_ALLOW_HTTP": "true", "WAVE_MASTER_KEY": randomHex(32),
		"AWS_ACCESS_KEY_ID": "wave-test", "AWS_SECRET_ACCESS_KEY": "wave-test-password", "WAVE_SANDBOX_BACKEND": "local", "WAVE_SANDBOX_ALLOW_LOCAL": "true", "WAVE_SANDBOX_LOCAL_ROOT": filepath.Join(work, "sandboxes"), "WAVE_PROFILE": "",
	}
	binary, cli := filepath.Join(work, "service"), filepath.Join(work, "wavectl")
	if err = t.run(ctx, t.root, nil, "go", "build", "-o", binary, "./tests/clients/service"); err != nil {
		return err
	}
	if err = t.run(ctx, t.path("cli"), nil, "go", "build", "-o", cli, "."); err != nil {
		return err
	}
	if err = t.run(ctx, t.path("sdks/typescript"), nil, "npm", "run", "build"); err != nil {
		return err
	}
	connection := filepath.Join(work, "connection.json")
	logPath := filepath.Join(work, "service.log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	service := t.command(ctx, t.root, env, binary, connection)
	service.Stdout = log
	service.Stderr = log
	if err = service.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	var serviceErr error
	go func() { serviceErr = service.Wait(); close(exited) }()
	defer func() {
		_ = service.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = service.Process.Kill()
			<-exited
		}
	}()
	var credentials struct {
		URL string `json:"url"`
		Key string `json:"key"`
	}
	if err = ready(ctx, func(context.Context) (bool, error) {
		select {
		case <-exited:
			logs, _ := os.ReadFile(logPath)
			return false, fmt.Errorf("test service exited (%v): %s", serviceErr, logs)
		default:
		}
		return readJSON(connection, &credentials) == nil && credentials.URL != "" && credentials.Key != "", nil
	}); err != nil {
		return err
	}
	env["WAVE_BASE_URL"] = credentials.URL
	env["WAVE_API_KEY"] = credentials.Key
	env["WAVE_CLIENT_E2E"] = "1"
	env["WAVE_CONFIG_FILE"] = filepath.Join(work, "cli-config.json")
	for _, args := range [][]string{
		{"go", "test", "-C", "sdks/go", "-run", "TestRealService", "-count=1", "."},
		{"uv", "run", "--project", "sdks/python", "--frozen", "python", "sdks/python/tests/e2e.py"},
		{"node", "tests/clients/e2e.mjs"},
	} {
		if err = t.run(ctx, t.root, env, args...); err != nil {
			return err
		}
	}
	if err = t.cliWorkflow(ctx, cli, env, work); err != nil {
		return err
	}
	fmt.Fprintln(t.out, "Real-service Go/Python/TS/CLI task approval, result, SSE and S3 file tests passed.")
	return nil
}

// cliWorkflow exercises the commands shipped in the external agent skill.
func (t *tool) cliWorkflow(ctx context.Context, binary string, env map[string]string, work string) error {
	var fixture object
	if err := readJSON(t.path("tests/clients/workflow.json"), &fixture); err != nil {
		return err
	}
	call := func(input string, expected int, args ...string) (any, error) {
		cmd := t.command(ctx, t.root, env, append([]string{binary}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		var out, diagnostics bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diagnostics
		err := cmd.Run()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				return nil, err
			}
		}
		if code != expected {
			return nil, fmt.Errorf("CLI %s: exit %d, expected %d: %s", strings.Join(args, " "), code, expected, diagnostics.String())
		}
		var result any
		if err = json.Unmarshal(out.Bytes(), &result); err != nil {
			return nil, fmt.Errorf("CLI %s: %w", args[0], err)
		}
		return result, nil
	}
	agent, err := call("", 0, "agents", "create", "--name", str(at(fixture, "agent", "name")), "--model", str(at(fixture, "agent", "model")), "--tools", "@"+t.path("skills/wave-client/assets/client-tool.json"))
	if err != nil {
		return err
	}
	session, err := call("", 0, "sessions", "create", "--title", str(at(fixture, "session", "title")))
	if err != nil {
		return err
	}
	action, err := call(str(fixture["input"]), 6, "tasks", "create", "--session", str(at(session, "id")), "--agent", str(at(agent, "id")), "--input-file", "-", "--idempotency-key", "cli-skill-workflow", "--wait")
	if err != nil {
		return err
	}
	task := str(at(action, "task", "id"))
	if task == "" {
		return fmt.Errorf("CLI task creation returned no task ID")
	}
	selected, err := call("", 0, "tasks", "get", task, "--select", "/id")
	if err != nil {
		return err
	}
	if selected != task {
		return fmt.Errorf("CLI selected wrong task ID")
	}
	actions := list(at(action, "required_actions"))
	if len(actions) != 1 || str(at(actions[0], "type")) != "approve_tool" {
		return fmt.Errorf("CLI expected approval action")
	}
	toolCall := str(at(actions[0], "call_id"))
	if _, err = call("", 0, "tools", "approve", toolCall, "--task", task); err != nil {
		return err
	}
	action, err = call("", 6, "tasks", "wait", task, "--interval", "10ms")
	if err != nil {
		return err
	}
	actions = list(at(action, "required_actions"))
	if len(actions) != 1 || str(at(actions[0], "type")) != "submit_tool_result" {
		return fmt.Errorf("CLI expected result action")
	}
	if _, err = call("", 0, "tools", "result", toolCall, "--task", task, "--result-file", "-"); err != nil {
		return err
	}
	result, err := call("", 0, "tasks", "wait", task, "--interval", "10ms")
	if err != nil {
		return err
	}
	if at(result, "task", "state") != "succeeded" {
		return fmt.Errorf("CLI task did not succeed")
	}
	uploaded, err := call(str(fixture["file"]), 0, "files", "upload", "--file", "-", "--filename", "中文.txt")
	if err != nil {
		return err
	}
	target := filepath.Join(work, "downloaded.txt")
	if _, err = call("", 0, "files", "download", str(at(uploaded, "id")), "--output", target); err != nil {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if string(data) != str(fixture["file"]) {
		return fmt.Errorf("CLI S3 download differs from uploaded content")
	}
	return nil
}
