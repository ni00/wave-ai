package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"wave-ai.local/wave/internal/platform/xid"
)

func (l *Local) Ensure(ctx context.Context, sessionID string) error {
	for _, dir := range []string{"/workspace", "/mnt/session/uploads", "/mnt/session/outputs", "/mnt/skills", "/mnt/memory"} {
		host, err := l.mapPath(sessionID, dir)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(host, 0o755); err != nil {
			return err
		}
	}
	// Uploads are stage targets for the platform; individual files are
	// written 0444 by StageInput. Unlike sbx there is no boundary the
	// directory mode could enforce (the agent shell runs as the same
	// user), so the directory itself stays writable — see the Local
	// docstring: development backend, isolation is per-directory only.
	return nil
}

func (l *Local) Exec(ctx context.Context, sessionID string, req ExecRequest) (*ExecResult, error) {
	cwd := req.Cwd
	if cwd == "" {
		cwd = "/workspace"
	}
	hostCwd, err := l.mapPath(sessionID, cwd)
	if err != nil {
		return nil, err
	}
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, javaDuration(req.TimeoutSec))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", l.translateCommand(sessionID, req.Command))
	cmd.Dir = hostCwd
	// Development backend hardening: strip the service environment so the
	// agent cannot read inherited credentials/DB URLs. Only a minimal,
	// deterministic PATH/HOME/TZ is provided (plus per-session HOME).
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + hostCwd,
		"TMPDIR=" + hostCwd + "/.tmp",
		"LANG=C.UTF-8",
		"TZ=UTC",
	}
	os.MkdirAll(hostCwd+"/.tmp", 0o700)
	// own process group so interrupt/timeout kills the whole tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	// cap retained output so a runaway command cannot exhaust memory
	stdout, stderr := &limitedBuffer{max: maxCaptureBytes}, &limitedBuffer{max: maxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	res := &ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			res.Stderr += "\n" + runErr.Error()
		}
	}
	return res, nil
}

func javaDuration(sec int) time.Duration {
	return time.Duration(sec) * time.Second
}

func (l *Local) ReadFile(ctx context.Context, sessionID, path string, maxBytes int64) ([]byte, error) {
	host, err := l.mapPath(sessionID, path)
	if err != nil {
		return nil, err
	}
	f, e := os.Open(host)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if maxBytes > 0 {
		return io.ReadAll(io.LimitReader(f, maxBytes))
	}
	return io.ReadAll(f)
}

func (l *Local) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	host, err := l.mapPath(sessionID, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		return err
	}
	return os.WriteFile(host, content, 0o644)
}

func (l *Local) ListDir(ctx context.Context, sessionID, path string) ([]string, error) {
	host, err := l.mapPath(sessionID, path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		suffix := ""
		if e.IsDir() {
			suffix = "/"
		}
		out = append(out, e.Name()+suffix)
	}
	return out, nil
}

func (l *Local) StageInput(ctx context.Context, sessionID, relPath string, content []byte) error {
	host, err := l.mapPath(sessionID, "/mnt/session/uploads/"+relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(host, content, 0o444); err != nil {
		return err
	}
	os.Chmod(host, 0o444)
	return nil
}

func (l *Local) StageSkill(ctx context.Context, sessionID, name string, files map[string][]byte) error {
	for rel, content := range files {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "../")
		host, err := l.mapPath(sessionID, "/mnt/skills/"+name+"/"+clean)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(host, content, 0o444); err != nil {
			return err
		}
	}
	return nil
}

func (l *Local) StageMemory(ctx context.Context, sessionID, mountPath, relPath string, content []byte, writable bool) error {
	host, err := l.mapPath(sessionID, mountPath+"/"+relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o444)
	if writable {
		perm = 0o644
	}
	if err := os.WriteFile(host, content, perm); err != nil {
		return err
	}
	os.Chmod(host, perm)
	return nil
}

// VisitOutputs exports regular files without retaining the full output tree.
func (l *Local) VisitOutputs(ctx context.Context, sid string, maxFileBytes int64, visit func(string, []byte) error) error {
	root, err := l.mapPath(sid, "/mnt/session/outputs")
	if err != nil {
		return err
	}
	if _, err = os.Stat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if maxFileBytes <= 0 {
		maxFileBytes = 32 << 20
	}
	total, count := int64(0), 0
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > maxFileBytes {
			return fmt.Errorf("artifact exceeds per-file limit")
		}
		count++
		if count > 1000 {
			return fmt.Errorf("artifact publication file limit exceeded")
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) > maxFileBytes {
			return fmt.Errorf("artifact exceeds per-file limit")
		}
		total += int64(len(data))
		if total > 128<<20 {
			return fmt.Errorf("artifact publication size limit exceeded")
		}
		return visit("/mnt/session/outputs/"+filepath.ToSlash(rel), data)
	})
}

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

func (l *Local) Stop(ctx context.Context, sessionID string) error {
	// local backend keeps the workspace dir (disk state retention).
	return nil
}

// translateCommand rewrites contract sandbox paths in command text to the
// host workspace paths (local dev backend only). The sbx backend runs
// inside the sandbox where these paths are real.
func (l *Local) translateCommand(sessionID, command string) string {
	replacements := []struct{ from, to string }{}
	for _, p := range []string{"/workspace", "/mnt/session/uploads", "/mnt/session/outputs", "/mnt/skills", "/mnt/memory"} {
		host, err := l.mapPath(sessionID, p)
		if err != nil {
			continue
		}
		replacements = append(replacements, struct{ from, to string }{p, host})
	}
	out := command
	// longest prefixes first so /mnt/session/uploads wins over /mnt/session
	for i := 0; i < len(replacements); i++ {
		for j := i + 1; j < len(replacements); j++ {
			if len(replacements[j].from) > len(replacements[i].from) {
				replacements[i], replacements[j] = replacements[j], replacements[i]
			}
		}
	}
	for _, r := range replacements {
		out = strings.ReplaceAll(out, r.from, r.to)
	}
	return out
}

func (l *Local) Identity(ctx context.Context, sid string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := filepath.Join(l.sessionRoot(sid), ".wave-workspace-id")
	raw, err := os.ReadFile(name)
	if err == nil {
		return string(raw), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		raw, err = os.ReadFile(name)
		return string(raw), err
	}
	if err != nil {
		return "", err
	}
	id := xid.New("workspace")
	_, err = f.WriteString(id)
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	return id, closeErr
}
