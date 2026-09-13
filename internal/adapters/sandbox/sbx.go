package sandbox

import (
	"context"

	"fmt"
	"net/http"

	"sync"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/v1"
	sbx "github.com/docker/sandboxes-api/gen/go/sbx"

	"gorm.io/gorm"
)

// SbxOptions configures the Docker Sandboxes backend.
type SbxOptions struct {
	BaseURL string
	Token   string
	Parent  string // sandbox grouping name
	// Image is the explicit sandbox image. It MUST be the platform's
	// non-Docker-Engine compatibility image; an image-level Docker daemon
	// would expose a host-control interface to agent code.
	Image     string
	CPUs      uint32
	MemoryMiB uint64
	Store     *gorm.DB
}

// Sbx implements Provider over the official Docker Sandboxes Go SDK.
//
// Production verification requires a real Docker Sandboxes daemon; see
// README.md. Capabilities are probed before execution.
type Sbx struct {
	opts      SbxOptions
	http      connect.HTTPClient
	mgmt      *sbx.Client
	st        *gorm.DB
	mu        sync.Mutex
	boxes     map[string]*sbxBox // sessionID -> resolved sandbox
	probed    bool
	lifecycle sync.Map // session ID -> *sync.Mutex
}

type sbxBox struct {
	ID       string
	Name     string
	Endpoint string
}

// maxCaptureBytes caps stdout/stderr retained per command; further output
// is discarded (callers truncate for the model anyway).
const maxCaptureBytes = 256 * 1024

func NewSbx(opts SbxOptions) *Sbx {
	var hc connect.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	if opts.Token != "" {
		hc = &authHTTP{inner: hc, token: opts.Token}
	}
	return &Sbx{
		opts:  opts,
		http:  hc,
		mgmt:  sbx.New(hc, opts.BaseURL),
		st:    opts.Store,
		boxes: map[string]*sbxBox{},
	}
}

func (s *Sbx) String() string { return "sbx" }

type authHTTP struct {
	inner connect.HTTPClient
	token string
}

func (a *authHTTP) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+a.token)
	return a.inner.Do(req)
}

// probe verifies the management endpoint once; fail-closed: without it no
// sandbox operation may run.
func (s *Sbx) probe(ctx context.Context) error {
	s.mu.Lock()
	if s.probed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	caps, err := s.mgmt.Capabilities().GetCapabilities(ctx, connect.NewRequest(&v1.GetCapabilitiesRequest{}))
	if err != nil {
		return fmt.Errorf("sbx capabilities probe failed (backend unavailable or unauthenticated): %w", err)
	}
	_ = caps

	s.mu.Lock()
	s.probed = true
	s.mu.Unlock()
	return nil
}

// Record persists only backend identity; execution owns business state.
type Record struct {
	SessionID string `gorm:"primaryKey"`
	BackendID string
	Name      string
	Endpoint  string
	State     string
	UpdatedAt time.Time
}

func (Record) TableName() string { return "sandbox_instances" }
