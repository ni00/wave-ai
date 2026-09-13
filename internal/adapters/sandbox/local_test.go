package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Regression: staging inputs must work after Ensure within the same turn
// (an earlier version chmod'd the uploads dir 0555 in Ensure, which made
// every subsequent StageInput fail with EACCES for non-root services).
func TestLocalStageInputAfterEnsure(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.Ensure(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := l.StageInput(ctx, "s1", "docs/a.txt", []byte("hi")); err != nil {
		t.Fatalf("stage input after ensure: %v", err)
	}
	got, err := l.ReadFile(ctx, "s1", "/mnt/session/uploads/docs/a.txt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Errorf("read back %q", got)
	}
}

func TestLocalVisitOutputsRespectsMaxFileBytes(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.WriteFile(ctx, "s1", "/mnt/session/outputs/small.txt", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteFile(ctx, "s1", "/mnt/session/outputs/big.bin", bytes.Repeat([]byte("x"), 100)); err != nil {
		t.Fatal(err)
	}
	if err := l.VisitOutputs(ctx, "s1", 50, func(string, []byte) error { return nil }); err == nil {
		t.Fatal("oversized artifact must fail publication explicitly")
	}
	out := map[string][]byte{}
	err = l.VisitOutputs(ctx, "s1", 200, func(p string, b []byte) error { out[p] = b; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || len(out["/mnt/session/outputs/big.bin"]) != 100 {
		t.Fatalf("incomplete export: %v", out)
	}

}

func TestLocalExecCapsCapturedOutput(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.Exec(context.Background(), "s1", ExecRequest{
		Command: "yes hello | head -c 10000000", TimeoutSec: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) > maxCaptureBytes+64 {
		t.Errorf("stdout not capped: %d bytes", len(res.Stdout))
	}
	if !bytes.Contains([]byte(res.Stdout), []byte("[output truncated]")) {
		t.Error("truncation marker missing")
	}
}

func TestPublicationStopsAtFirstRejectedFile(t *testing.T) {
	ctx := context.Background()
	local, _ := NewLocal(t.TempDir())
	if e := local.Ensure(ctx, "session"); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"a", "b"} {
		if e := local.WriteFile(ctx, "session", "/mnt/session/outputs/"+name, []byte("content")); e != nil {
			t.Fatal(e)
		}
	}
	calls := 0
	rejected := errors.New("upload failed")
	e := local.VisitOutputs(ctx, "session", 100, func(string, []byte) error { calls++; return rejected })
	if !errors.Is(e, rejected) || calls != 1 {
		t.Fatal("publication continued after failure", e, calls)
	}
}
func TestWorkspaceIdentityChangesAfterDiskReplacement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	local, _ := NewLocal(root)
	if e := local.Ensure(ctx, "session"); e != nil {
		t.Fatal(e)
	}
	id, e := local.Identity(ctx, "session")
	if e != nil || id == "" {
		t.Fatal(e)
	}
	if e = local.Stop(ctx, "session"); e != nil {
		t.Fatal(e)
	}
	if e = local.Ensure(ctx, "session"); e != nil {
		t.Fatal(e)
	}
	same, e := local.Identity(ctx, "session")
	if e != nil || same != id {
		t.Fatal("restart lost disk identity", e)
	}
	if e = os.RemoveAll(filepath.Join(root, "session")); e != nil {
		t.Fatal(e)
	}
	if e = local.Ensure(ctx, "session"); e != nil {
		t.Fatal(e)
	}
	replacement, e := local.Identity(ctx, "session")
	if e != nil || replacement == id {
		t.Fatal("replacement reused identity", e)
	}
}
