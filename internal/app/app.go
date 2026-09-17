// Package app is the composition root. Business packages own their models and routes.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"

	apicontract "wave-ai.local/wave/api"
	"wave-ai.local/wave/internal/adapters/mcpclient"
	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/console"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/guardrails"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/modules/vault"
	"wave-ai.local/wave/internal/modules/webhooks"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/config"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/secrets"
	"wave-ai.local/wave/internal/platform/telemetry"
	"wave-ai.local/wave/internal/platform/webui"
)

type App struct {
	Logs    *telemetry.LogSink
	MCP     mcpclient.Pool
	Guard   *guardrails.Set
	Config  *config.Config
	DB      *gorm.DB
	Blobs   *blobstore.Store
	Box     *secrets.Box
	Sandbox sandbox.Provider
}

func Models() []any {
	out := auth.Models()
	out = append(out, &agents.Agent{}, &agents.Version{}, &environments.Environment{}, &files.File{}, &vault.Credential{}, &vault.Vault{}, &skills.Skill{}, &deployments.Deployment{}, &deployments.Run{}, &deployments.Version{}, &sandbox.Record{}, &sandbox.Process{}, &Workspace{})
	out = append(out, memory.Models()...)
	return append(out, execution.Models()...)
}
func New(ctx context.Context, cfg *config.Config) (*App, error) {
	if cfg.SandboxBackend != "" && cfg.SandboxBackend != "gvisor" {
		return nil, fmt.Errorf("sandbox backend must be gvisor")
	}

	if cfg.StorageBackend == "s3" && len(cfg.MasterKey) != 32 {
		return nil, fmt.Errorf("S3 storage requires WAVE_MASTER_KEY (32-byte hex); keep the same key across restarts and replicas")
	}
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		return nil, e
	}
	pool, _ := db.DB()
	ok := false
	defer func() {
		if !ok {
			pool.Close()
		}
	}()
	var b *blobstore.Store
	switch cfg.StorageBackend {
	case "", "local":
		b, e = blobstore.New(cfg.DataDir)
	case "s3":
		b, e = blobstore.NewS3(ctx, cfg.S3)
	default:
		return nil, fmt.Errorf("unsupported storage backend")
	}
	if e != nil {
		return nil, e
	}
	box, e := secrets.NewBox(cfg.MasterKey, cfg.DataDir+"/master.key")
	if e != nil {
		return nil, e
	}
	provider, e := sandbox.NewContainer(sandbox.ContainerOptions{
		Backend: "gvisor", Host: cfg.DockerHost, Image: cfg.ContainerImage,
		Runtime: cfg.GVisorRuntime, Network: cfg.ContainerNetwork,
		CPUs: uint32(cfg.SandboxCPUs), MemoryMiB: uint64(cfg.SandboxMemoryMiB),
		MaxRunning: cfg.SandboxMaxRunning, MemoryBudgetMiB: uint64(cfg.SandboxMemoryBudgetMiB), Store: db,
		WorkspaceGiB: cfg.SandboxWorkspaceGiB})
	if e != nil {
		return nil, e
	}
	guard, e := guardrails.New(cfg.GuardrailInputBlock, cfg.GuardrailOutputRedact)
	if e != nil {
		return nil, e
	}
	ok = true
	return &App{Logs: telemetry.NewLogSink(db), Guard: guard, Config: cfg, DB: db, Blobs: b, Box: box, Sandbox: provider}, nil
}
func (a *App) Close() {
	if a.Logs != nil {
		a.Logs.Close()
	}
	a.MCP.Close()
	if c, ok := a.Sandbox.(interface{ Close() error }); ok {
		_ = c.Close()
	}
	pool, _ := a.DB.DB()
	_ = pool.Close()
}

// Handler exposes the routes documented by api/openapi.json.
func (a *App) Handler() http.Handler {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	_ = r.SetTrustedProxies(nil)
	r.Use(httpx.Observe())
	webui.Register(r)
	swaggerUI := http.StripPrefix("/swagger", swaggerFiles.Handler)
	r.GET("/swagger/*any", func(c *gin.Context) {
		switch c.Param("any") {
		case "/openapi.json", "/openapi.en.json":
			c.Data(http.StatusOK, "application/json; charset=utf-8", apicontract.OpenAPI)
			return
		case "/openapi.zh-CN.json":
			c.Data(http.StatusOK, "application/json; charset=utf-8", apicontract.OpenAPIChinese)
			return
		case "/swagger-initializer.js":
			c.Data(http.StatusOK, "application/javascript; charset=utf-8", apicontract.SwaggerInitializer)
			return
		}
		swaggerUI.ServeHTTP(c.Writer, c.Request)
	})
	r.NoRoute(func(c *gin.Context) { httpx.Error(c, apierr.NotFoundErr("route not found")) })
	r.NoMethod(func(c *gin.Context) {
		httpx.Error(c, apierr.New(http.StatusMethodNotAllowed, apierr.InvalidRequest, "method not allowed"))
	})
	r.GET("/health", a.health)
	v := r.Group("/v1", httpx.Authenticate(a.DB))
	v.Use(func(c *gin.Context) {
		if a.Logs != nil {
			p := c.MustGet("principal").(*auth.Principal)
			s := observe.From(c.Request.Context())
			s.OrgID, s.OwnerID, s.WriteLog = p.OrgID, p.PrincipalID, a.Logs.Write
			c.Request = c.Request.WithContext(observe.With(c.Request.Context(), s))
		}
		if c.ContentType() != "multipart/form-data" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		}
		c.Next()
	})
	console.Register(v, a.DB, a.Blobs)
	agents.Register(v, a.DB)
	environments.Register(v, a.DB)
	execution.Register(v, a.DB)
	files.Register(v, a.DB, a.Blobs)
	memory.Register(v, a.DB)
	vault.Register(v, a.DB, a.Box)
	skills.Register(v, a.DB, a.Blobs)
	deployments.Register(v, a.DB)
	webhooks.Register(v, a.DB, a.Box)
	return r
}

// health handles the documented HTTP operation.
func (a *App) health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	pool, _ := a.DB.DB()
	if e := pool.PingContext(ctx); e != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Status(http.StatusNoContent)
}
func (a *App) Run(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error { telemetry.Run(ctx, a.DB); return nil })
	role := a.Config.Role
	if role == "all" || role == "api" {
		group.Go(func() error { return a.serve(ctx) })
	}
	if role == "all" || role == "scheduler" {
		group.Go(func() error { webhooks.Run(ctx, a.DB, a.Box); return nil })
		group.Go(func() error { deployments.RunScheduler(ctx, a.DB); return nil })
	}
	if role == "all" || role == "worker" {
		group.Go(func() error { execution.Maintain(ctx, a.DB); return nil })
		for range a.Config.WorkerConcurrency {
			group.Go(func() error {
				worker := &execution.Worker{
					WriteLog:      a.Logs.Write,
					DB:            a.DB,
					Guard:         a.Guard,
					ContextTokens: a.Config.ContextTokens,
					Model:         modelclient.New(a.Config.ModelBaseURL, a.Config.ModelAPIKey, time.Duration(a.Config.ModelTimeoutSec)*time.Second),
					Lease:         time.Duration(a.Config.LeaseSeconds) * time.Second,
					Execute:       a.execute,
					Prepare:       a.prepare,
					Finish:        a.finish,
					Evaluate:      a.evaluate,
					Admit:         a.admitSandbox,
				}
				worker.Run(ctx)
				return nil
			})
		}
	}
	return group.Wait()
}
func (a *App) serve(ctx context.Context) error {
	server := &http.Server{
		Addr:              a.Config.HTTPAddr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// Generous body budget: uploads stream up to 32 MiB. WriteTimeout
		// stays unset on purpose; SSE responses manage their own deadlines.
		ReadTimeout: 5 * time.Minute,
		IdleTimeout: 120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := server.Shutdown(shutdown); e != nil {
			_ = server.Close()
			return e
		}
		return nil
	case e := <-result:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	}
}
