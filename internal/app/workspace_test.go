package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/platform/config"
)

// workspaceFixture records writes for app orchestration tests. It cannot execute
// commands or access host files; real sandbox behavior is tested through gVisor.
type workspaceFixture struct {
	sandbox.Provider
	mu    sync.Mutex
	files map[string]map[string][]byte
}

func useWorkspaceFixture(t *testing.T, a *App) {
	t.Helper()
	if closer, ok := a.Sandbox.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	a.Sandbox = &workspaceFixture{files: map[string]map[string][]byte{}}
}

func (f *workspaceFixture) String() string { return "test fixture" }
func (f *workspaceFixture) Ensure(ctx context.Context, sid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files[sid] == nil {
		f.files[sid] = map[string][]byte{}
	}
	return ctx.Err()
}
func (f *workspaceFixture) Identity(ctx context.Context, sid string) (string, error) {
	return "fixture:" + sid, ctx.Err()
}
func (f *workspaceFixture) Stop(ctx context.Context, _ string) error { return ctx.Err() }
func (f *workspaceFixture) Exec(context.Context, string, sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	return nil, errors.New("workspace fixture does not execute commands")
}
func (f *workspaceFixture) WriteFile(ctx context.Context, sid, name string, content []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.files[sid] == nil {
		return errors.New("workspace was not prepared")
	}
	f.files[sid][name] = append([]byte(nil), content...)
	return nil
}
func (f *workspaceFixture) VisitOutputs(ctx context.Context, sid string, limit int64, visit func(string, []byte) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, content := range f.files[sid] {
		if !strings.HasPrefix(name, "/mnt/session/outputs/") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if int64(len(content)) > limit {
			return errors.New("fixture output exceeds publication limit")
		}
		if err := visit(name, content); err != nil {
			return err
		}
	}
	return nil
}

func TestUnsupportedSandboxFailsBeforeDatabase(t *testing.T) {
	for _, backend := range []string{"local", "unknown"} {
		_, err := New(context.Background(), &config.Config{SandboxBackend: backend, DatabaseURL: "invalid"})
		if err == nil || err.Error() != "sandbox backend must be gvisor" {
			t.Fatalf("unsupported backend %q: %v", backend, err)
		}
	}
}
