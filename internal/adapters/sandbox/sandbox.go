// Package sandbox defines the Provider abstraction for session workspaces
// and process/file operations.
//
// Backends must map the contract paths:
//
//	/workspace              agent's writable working directory
//	/mnt/session/uploads    read-only input mounts
//	/mnt/session/outputs    writable, cataloged artifacts
//	/mnt/skills/*           skill package projections
package sandbox

import (
	"bytes"
	"context"
	"fmt"
)

// ExecRequest describes a process to run in the session workspace.
type ExecRequest struct {
	Command    string // bash -c payload
	Cwd        string // sandbox-absolute; default /workspace
	TimeoutSec int
	// RequestID is a stable caller-assigned identity used by backends that
	// deduplicate execution retries.
	RequestID string
}

// ExecResult is the completed process outcome.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Provider is the execution backend. One instance serves operations for each
// session ID at a time; Ensure provisions, Stop releases compute keeping disk.
type Provider interface {
	// Ensure provisions the workspace (idempotent).
	Ensure(ctx context.Context, sessionID string) error
	// Identity identifies retained disk state; a replaced workspace has a new identity.
	Identity(ctx context.Context, sessionID string) (string, error)
	// Exec runs a command to completion and returns its output.
	Exec(ctx context.Context, sessionID string, req ExecRequest) (*ExecResult, error)
	// ReadFile returns the bytes at a sandbox-absolute path.
	ReadFile(ctx context.Context, sessionID, path string, maxBytes int64) ([]byte, error)
	// WriteFile writes bytes at a sandbox-absolute path.
	WriteFile(ctx context.Context, sessionID, path string, content []byte) error
	// ListDir lists entries of a sandbox-absolute directory.
	ListDir(ctx context.Context, sessionID, path string) ([]string, error)
	// StageInput places read-only input content under the uploads root.
	StageInput(ctx context.Context, sessionID, relPath string, content []byte) error
	// StageSkill places a skill package directory under /mnt/skills/<name>.
	StageSkill(ctx context.Context, sessionID, name string, files map[string][]byte) error
	// VisitOutputs publishes one bounded file at a time. Returning an error from
	// visit stops publication immediately; already published artifacts stay valid.
	VisitOutputs(ctx context.Context, sessionID string, maxFileBytes int64, visit func(string, []byte) error) error
	// Stop releases compute while retaining disk state.
	Stop(ctx context.Context, sessionID string) error
	fmt.Stringer
}

// maxCaptureBytes bounds captured command output.
const maxCaptureBytes = 256 * 1024

// limitedBuffer caps in-memory command output; further bytes are discarded.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() >= b.max {
		return len(p), nil
	}
	if b.buf.Len()+len(p) >= b.max {
		// fill to the cap and mark the truncation; later writes are dropped
		b.buf.Write(p[:b.max-b.buf.Len()])
		b.buf.WriteString("\n…[output truncated]")
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string { return b.buf.String() }
