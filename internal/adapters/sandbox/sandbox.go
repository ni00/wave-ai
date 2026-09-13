// Package sandbox defines the Provider abstraction for session workspaces
// and process/file operations, plus the local development backend.
//
// Backends must map the contract paths:
//
//	/workspace              agent's writable working directory
//	/mnt/session/uploads    read-only input mounts
//	/mnt/session/outputs    writable, cataloged artifacts
//	/mnt/memory/*           memory store projections
//	/mnt/skills/*           skill package projections
package sandbox

import (
	"context"
	"fmt"
)

// ExecRequest describes a process to run in the session workspace.
type ExecRequest struct {
	Command    string // bash -c payload
	Cwd        string // sandbox-absolute; default /workspace
	TimeoutSec int
	// RequestID is a stable caller-assigned identity used by backends that
	// deduplicate create retries (sbx CreateProcess.request_id).
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
	// StageMemory places memory files under a memory mount path.
	StageMemory(ctx context.Context, sessionID, mountPath, relPath string, content []byte, writable bool) error
	// VisitOutputs publishes one bounded file at a time. Returning an error from
	// visit stops publication immediately; already published artifacts stay valid.
	VisitOutputs(ctx context.Context, sessionID string, maxFileBytes int64, visit func(string, []byte) error) error
	// Stop releases compute while retaining disk state.
	Stop(ctx context.Context, sessionID string) error
	fmt.Stringer
}

// ---------------------------------------------------------------- local

// Local runs session workspaces as directories on the host with commands
// executed by the host shell in the workspace cwd. It is a DEVELOPMENT
// backend: isolation is only per-directory; never expose it to untrusted
// agent configurations in production.
type Local struct {
	Root string // parent dir holding one subdir per session
}

func NewLocal(root string) (*Local, error) {
	l := &Local{Root: root}
	return l, nil
}

func (l *Local) String() string { return "local" }

func (l *Local) sessionRoot(sessionID string) string {
	return l.Root + "/" + sessionID
}

// mapPath translates a contract path into a host path under the session root.
func (l *Local) mapPath(sessionID, p string) (string, error) {
	if len(p) == 0 || p[0] != '/' {
		return "", fmt.Errorf("path must be absolute: %s", p)
	}
	clean := p
	// collapse .. and .
	parts := splitPath(clean)
	var out []string
	for _, seg := range parts {
		switch seg {
		case ".":
		case "..":
			if len(out) == 0 {
				return "", fmt.Errorf("path escapes workspace: %s", p)
			}
			out = out[:len(out)-1]
		default:
			out = append(out, seg)
		}
	}
	return l.sessionRoot(sessionID) + "/" + joinPath(out), nil
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for _, r := range p {
		if r == '/' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func joinPath(parts []string) string {
	res := ""
	for _, p := range parts {
		res += "/" + p
	}
	return res
}
