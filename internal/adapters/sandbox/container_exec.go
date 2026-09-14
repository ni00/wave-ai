package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/xid"
)

// Process records an intent before contacting the engine. Docker exec has no
// idempotency token: an ambiguous start is never automatically replayed.
type Process struct {
	SessionID string `gorm:"primaryKey"`
	RequestID string `gorm:"primaryKey"`
	Digest    string `gorm:"not null"`
	ExecID    string
	State     string `gorm:"not null"`
	Stdout    string
	Stderr    string
	ExitCode  int
}

func (Process) TableName() string { return "sandbox_processes" }

func (c *Container) Exec(ctx context.Context, sid string, req ExecRequest) (*ExecResult, error) {
	defer c.lock(sid)()
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 120
	}
	if req.TimeoutSec > 3600 {
		return nil, errors.New("sandbox command timeout exceeds 3600 seconds")
	}
	if req.Cwd == "" {
		req.Cwd = "/workspace"
	}
	if err := absolutePath(req.Cwd); err != nil {
		return nil, err
	}
	if req.RequestID == "" {
		req.RequestID = xid.New("exec")
	}
	raw, _ := json.Marshal(req)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	intent := Process{SessionID: sid, RequestID: req.RequestID, Digest: digest, State: "starting"}
	inserted := c.opts.Store.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&intent)
	if inserted.Error != nil {
		return nil, inserted.Error
	}
	key := []clause.Expression{clause.Eq{Column: "session_id", Value: sid}, clause.Eq{Column: "request_id", Value: req.RequestID}}
	if inserted.RowsAffected == 0 {
		if err := c.opts.Store.WithContext(ctx).Where(clause.And(key...)).Take(&intent).Error; err != nil {
			return nil, err
		}
		if intent.Digest != digest {
			return nil, errors.New("exec request ID reused with a different command")
		}
		if intent.State != "completed" {
			return nil, errors.New("sandbox execution outcome unknown; request will not be replayed")
		}
		return &ExecResult{Stdout: intent.Stdout, Stderr: intent.Stderr, ExitCode: intent.ExitCode}, nil
	}
	r, err := c.ensure(ctx, sid)
	if err != nil {
		return nil, err
	}
	run, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()
	created, err := c.api.ExecCreate(run, r.BackendID, client.ExecCreateOptions{User: "0:0", AttachStdout: true, AttachStderr: true, WorkingDir: req.Cwd, Cmd: []string{"timeout", "--signal=KILL", strconv.Itoa(req.TimeoutSec), "bash", "-c", req.Command}})
	if err != nil {
		return nil, err
	}
	if err = c.opts.Store.WithContext(run).Model(&Process{}).Where(clause.And(key...)).Update("exec_id", created.ID).Error; err != nil {
		return nil, err
	}
	attached, err := c.api.ExecAttach(run, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, errors.Join(err, c.killAndConfirm(r))
	}
	defer attached.Close()
	stdout, stderr := &limitedBuffer{max: maxCaptureBytes}, &limitedBuffer{max: maxCaptureBytes}
	done := make(chan error, 1)
	go func() { _, e := stdcopy.StdCopy(stdout, stderr, attached.Reader); done <- e }()
	select {
	case err = <-done:
	case <-run.Done():
		attached.Close()
		<-done
		return nil, errors.Join(run.Err(), c.killAndConfirm(r))
	}
	if err != nil {
		return nil, errors.Join(err, c.killAndConfirm(r))
	}
	inspected, err := c.api.ExecInspect(run, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, errors.Join(err, c.killAndConfirm(r))
	}
	if inspected.Running {
		return nil, errors.Join(errors.New("sandbox exec detached unexpectedly"), c.killAndConfirm(r))
	}
	// A timeout utility exit can race the host deadline. Kill the entire sandbox
	// on timeout so detached children cannot survive the cancelled operation.
	if inspected.ExitCode == 124 || inspected.ExitCode == 137 {
		if err = c.killAndConfirm(r); err != nil {
			return nil, err
		}
	}
	result := &ExecResult{Stdout: captureText(stdout.String()), Stderr: captureText(stderr.String()), ExitCode: inspected.ExitCode}
	updates := map[string]any{"state": "completed", "stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode}
	if err = c.opts.Store.WithContext(ctx).Model(&Process{}).Where(clause.And(key...)).Updates(updates).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Container) killAndConfirm(r Record) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := c.api.ContainerInspect(ctx, r.BackendID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if err = c.verify(info.Container, r, false); err != nil {
		return err
	}
	if info.Container.State != nil && info.Container.State.Running {
		if _, err = c.api.ContainerKill(ctx, r.BackendID, client.ContainerKillOptions{Signal: "SIGKILL"}); err != nil {
			return err
		}
	}
	for {
		info, err = c.api.ContainerInspect(ctx, r.BackendID, client.ContainerInspectOptions{})
		if err != nil {
			return err
		}
		if info.Container.State != nil && !info.Container.State.Running {
			return c.state(ctx, r.SessionID, "stopped")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Destroy is explicit disk deletion, used only for disposable benchmark
// sessions. Normal task completion calls Stop and retains all session data.
func (c *Container) Destroy(ctx context.Context, sid string) error {
	defer c.lock(sid)()
	r, err := c.record(ctx, sid)
	if err != nil {
		return err
	}
	for _, stager := range []bool{false, true} {
		ref := r.Name
		if stager {
			ref += "-stager"
		}
		info, e := c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(e) {
			continue
		}
		if e != nil {
			return e
		}
		if e = c.verify(info.Container, r, stager); e != nil {
			return e
		}
		if _, e = c.api.ContainerRemove(ctx, info.Container.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); e != nil {
			return e
		}
	}
	for _, kind := range []string{"uploads", "skills"} {
		v, e := c.api.VolumeInspect(ctx, volumeName(r, kind), client.VolumeInspectOptions{})
		if errdefs.IsNotFound(e) {
			continue
		}
		if e != nil {
			return e
		}
		if v.Volume.Labels["wave.session"] != sid {
			return errors.New("refusing to delete unowned volume")
		}
		if _, e = c.api.VolumeRemove(ctx, volumeName(r, kind), client.VolumeRemoveOptions{}); e != nil {
			return e
		}
	}
	if err = c.opts.Store.WithContext(ctx).Where(clause.Eq{Column: "session_id", Value: sid}).Delete(&Process{}).Error; err != nil {
		return err
	}
	return c.opts.Store.WithContext(ctx).Where(clause.Eq{Column: "session_id", Value: sid}).Delete(&Record{}).Error
}

// Tool output is text stored in PostgreSQL. Preserve a bounded UTF-8 response
// even when a process emits binary bytes or NUL, which PostgreSQL text rejects.
func captureText(raw string) string {
	text := strings.ToValidUTF8(strings.ReplaceAll(raw, "\x00", "�"), "�")
	b := &limitedBuffer{max: maxCaptureBytes}
	_, _ = b.Write([]byte(text))
	return strings.ToValidUTF8(b.String(), "�")
}
