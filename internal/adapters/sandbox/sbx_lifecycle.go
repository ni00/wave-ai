package sandbox

import (
	"context"
	"errors"
	"fmt"

	"sync"
	"time"

	"connectrpc.com/connect"
	sbxcommonv1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/common/v1"
	sbxfilesv1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/files/v1"

	v1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/v1"
	sbx "github.com/docker/sandboxes-api/gen/go/sbx"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/xid"
)

func (s *Sbx) refByID(b *sbxBox) *sbxcommonv1.SandboxRef {
	return &sbxcommonv1.SandboxRef{
		Parent:     s.opts.Parent,
		Identifier: &sbxcommonv1.SandboxRef_Id{Id: b.ID},
	}
}

// waitOperation polls an operation to its terminal state; fails on error.
func (s *Sbx) waitOperation(ctx context.Context, opID string) error {
	for {
		wait, err := s.mgmt.Operations().WaitOperation(ctx, connect.NewRequest(&v1.WaitOperationRequest{
			Id:      opID,
			Timeout: durationPtr(30 * time.Second),
		}))
		if err != nil {
			return fmt.Errorf("sbx wait operation: %w", err)
		}
		if wait.Msg.Done {
			if wait.Msg.GetError() != nil {
				return fmt.Errorf("sbx operation failed: %v", wait.Msg.GetError().String())
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

// fetchSandbox loads current sandbox state by stable ID.
func (s *Sbx) fetchSandbox(ctx context.Context, b *sbxBox) (*v1.Sandbox, error) {
	sb, err := s.mgmt.Sandboxes().GetSandbox(ctx, connect.NewRequest(&v1.GetSandboxRequest{
		Sandbox: s.refByID(b),
	}))
	if err != nil {
		return nil, err
	}
	return sb.Msg, nil
}

// ensureRunning returns a sandbox in RUNNING state, starting a stopped one
// and waiting for the terminal state, refreshing the endpoint (which may
// change across stop/start).
func (s *Sbx) ensureRunning(ctx context.Context, sessionID string, b *sbxBox) error {
	sb, err := s.fetchSandbox(ctx, b)
	if err != nil {
		return fmt.Errorf("sbx get: %w", err)
	}
	switch sb.GetCore().GetStatus() {
	case sbxcommonv1.SandboxStatus_SANDBOX_STATUS_RUNNING:
	case sbxcommonv1.SandboxStatus_SANDBOX_STATUS_STOPPED, sbxcommonv1.SandboxStatus_SANDBOX_STATUS_FAILED:
		op, err := s.mgmt.Sandboxes().StartSandbox(ctx, connect.NewRequest(&v1.StartSandboxRequest{
			Sandbox:   s.refByID(b),
			RequestId: xid.New("start"),
		}))
		if err != nil {
			return fmt.Errorf("sbx start: %w", err)
		}
		if err := s.waitOperation(ctx, op.Msg.Id); err != nil {
			return err
		}
		sb, err = s.fetchSandbox(ctx, b)
		if err != nil {
			return err
		}
		if sb.GetCore().GetStatus() != sbxcommonv1.SandboxStatus_SANDBOX_STATUS_RUNNING {
			return fmt.Errorf("sbx start did not reach RUNNING (now %s)", sb.GetCore().GetStatus())
		}
	case sbxcommonv1.SandboxStatus_SANDBOX_STATUS_CREATING, sbxcommonv1.SandboxStatus_SANDBOX_STATUS_STARTING:
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if sb, err = s.fetchSandbox(ctx, b); err != nil {
				return err
			}
			if sb.GetCore().GetStatus() == sbxcommonv1.SandboxStatus_SANDBOX_STATUS_RUNNING {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		if sb.GetCore().GetStatus() != sbxcommonv1.SandboxStatus_SANDBOX_STATUS_RUNNING {
			return fmt.Errorf("sbx still %s after wait", sb.GetCore().GetStatus())
		}
	default:
		return fmt.Errorf("sbx sandbox in unusable state %s", sb.GetCore().GetStatus())
	}
	if ep := sb.GetCore().GetEndpoint().GetUri(); ep != "" && ep != b.Endpoint {
		b.Endpoint = ep
		s.mu.Lock()
		s.boxes[sessionID] = b
		s.mu.Unlock()
		if err := s.st.WithContext(ctx).Model(&Record{}).Where(clause.Eq{Column: "session_id", Value: sessionID}).Updates(map[string]any{"backend_id": b.ID, "endpoint": b.Endpoint, "state": "running"}).Error; err != nil {
			return err
		}
	}
	return nil
}

// resolve returns the sandbox for a session, creating it on first use with
// the explicit run spec (non-Docker-Engine image + resources).
func (s *Sbx) resolve(ctx context.Context, sessionID string) (*sbxBox, error) {
	s.mu.Lock()
	if b, ok := s.boxes[sessionID]; ok {
		s.mu.Unlock()
		return b, nil
	}
	s.mu.Unlock()

	// persisted mapping (survives process restarts)
	var record Record
	err := s.st.WithContext(ctx).Where(clause.Eq{Column: "session_id", Value: sessionID}).Take(&record).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil && record.BackendID != "" {
		b := &sbxBox{ID: record.BackendID, Name: record.Name, Endpoint: record.Endpoint}
		s.mu.Lock()
		s.boxes[sessionID] = b
		s.mu.Unlock()
		return b, nil
	}
	name := ""

	name = "wave-" + sessionID
	cpus := s.opts.CPUs
	mem := s.opts.MemoryMiB
	op, err := s.mgmt.Sandboxes().CreateSandbox(ctx, connect.NewRequest(&v1.CreateSandboxRequest{
		Parent:    s.opts.Parent,
		Agent:     "shell",
		Name:      name,
		Image:     s.opts.Image,
		Resources: &sbxcommonv1.Resources{Cpus: &cpus, MemoryMib: &mem},
		RequestId: "ensure-" + sessionID,
	}))
	if err != nil {
		return nil, fmt.Errorf("sbx create: %w", err)
	}
	if err := s.waitOperation(ctx, op.Msg.Id); err != nil {
		return nil, err
	}
	sb, err := s.mgmt.Sandboxes().GetSandbox(ctx, connect.NewRequest(&v1.GetSandboxRequest{
		Sandbox: &sbxcommonv1.SandboxRef{
			Parent:     s.opts.Parent,
			Identifier: &sbxcommonv1.SandboxRef_Name{Name: name},
		},
	}))
	if err != nil {
		return nil, fmt.Errorf("sbx get after create: %w", err)
	}
	ep := sb.Msg.GetCore().GetEndpoint().GetUri()
	if ep == "" {
		return nil, fmt.Errorf("sbx sandbox has no endpoint")
	}
	b := &sbxBox{ID: sb.Msg.GetCore().GetId(), Name: name, Endpoint: ep}
	record = Record{SessionID: sessionID, BackendID: b.ID, Name: name, Endpoint: ep, State: "provisioned"}
	err = s.st.WithContext(ctx).Save(&record).Error
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.boxes[sessionID] = b
	s.mu.Unlock()
	return b, nil
}

func (s *Sbx) client(ctx context.Context, sessionID string) (*sbx.SandboxClient, error) {
	mutex, _ := s.lifecycle.LoadOrStore(sessionID, &sync.Mutex{})
	lock := mutex.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if err := s.probe(ctx); err != nil {
		return nil, err
	}
	b, err := s.resolve(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureRunning(ctx, sessionID, b); err != nil {
		return nil, err
	}

	return sbx.NewSandboxClient(s.http, b.Endpoint), nil
}

// Ensure provisions (or restarts) the workspace with the contract dirs.
func (s *Sbx) Ensure(ctx context.Context, sessionID string) error {
	sc, err := s.client(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, dir := range []string{"/workspace", "/mnt/session/uploads", "/mnt/session/outputs", "/mnt/skills", "/mnt/memory"} {
		if _, err := sc.Files().Mkdir(ctx, connect.NewRequest(&sbxfilesv1.MkdirRequest{Path: dir, Parents: true})); err != nil {
			return fmt.Errorf("sbx mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// Stop releases compute while retaining disk: waits for the stop
// operation's terminal state before recording it; unknown outcomes are
// never marked stopped.
func (s *Sbx) Stop(ctx context.Context, sessionID string) error {
	mutex, _ := s.lifecycle.LoadOrStore(sessionID, &sync.Mutex{})
	lock := mutex.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if err := s.probe(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	b, ok := s.boxes[sessionID]
	if ok {
	}
	s.mu.Unlock()
	if !ok {
		var record Record
		err := s.st.WithContext(ctx).Where(clause.Eq{Column: "session_id", Value: sessionID}).Take(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if record.BackendID == "" {
			return nil
		}
		b = &sbxBox{ID: record.BackendID, Name: record.Name}

	}
	op, err := s.mgmt.Sandboxes().StopSandbox(ctx, connect.NewRequest(&v1.StopSandboxRequest{
		Sandbox:   s.refByID(b),
		RequestId: xid.New("stop"),
	}))
	if err != nil {
		return fmt.Errorf("sbx stop: %w", err)
	}
	if err := s.waitOperation(ctx, op.Msg.Id); err != nil {
		return fmt.Errorf("sbx stop not confirmed: %w", err)
	}

	actual, err := s.fetchSandbox(ctx, b)
	if err != nil {
		return err
	}
	if actual.GetCore().GetStatus() != sbxcommonv1.SandboxStatus_SANDBOX_STATUS_STOPPED {
		return fmt.Errorf("sandbox stop not confirmed: %s", actual.GetCore().GetStatus())
	}
	return s.st.WithContext(ctx).Model(&Record{}).Where(clause.Eq{Column: "session_id", Value: sessionID}).Update("state", "stopped").Error
}

func durationPtr(d time.Duration) *durationpb.Duration {
	return durationpb.New(d)
}

func (s *Sbx) Identity(ctx context.Context, sid string) (string, error) {
	b, e := s.resolve(ctx, sid)
	if e != nil {
		return "", e
	}
	return b.ID, nil
}
