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
	ginSwagger "github.com/swaggo/gin-swagger"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"

	apicontract "wave-ai.local/wave/api"
	"wave-ai.local/wave/internal/adapters/mcpclient"
	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/guardrails"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/modules/vault"
	_ "wave-ai.local/wave/internal/platform/apidocs" // 生成的 OpenAPI spec，/swagger 路由依赖
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/config"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/secrets"
)

type App struct {
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
	out = append(out, &agents.Agent{}, &agents.Version{}, &environments.Environment{}, &files.File{}, &vault.Credential{}, &skills.Skill{}, &deployments.Deployment{}, &deployments.Run{}, &sandbox.Record{}, &Workspace{})
	out = append(out, memory.Models()...)
	return append(out, execution.Models()...)
}
func New(ctx context.Context, cfg *config.Config) (*App, error) {
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
	var provider sandbox.Provider
	if cfg.SandboxBackend == "local" {
		provider, e = sandbox.NewLocal(cfg.SandboxLocalRoot)
	} else {
		provider = sandbox.NewSbx(sandbox.SbxOptions{
			BaseURL:   cfg.SbxURL,
			Token:     cfg.SbxToken,
			Image:     cfg.SbxImage,
			Parent:    "wave",
			CPUs:      uint32(cfg.SbxCPUs),
			MemoryMiB: uint64(cfg.SbxMemoryMiB),
			Store:     db,
		})
	}
	if e != nil {
		return nil, e
	}
	guard, e := guardrails.New(cfg.GuardrailInputBlock, cfg.GuardrailOutputRedact)
	if e != nil {
		return nil, e
	}
	ok = true
	return &App{Guard: guard, Config: cfg, DB: db, Blobs: b, Box: box, Sandbox: provider}, nil
}
func (a *App) Close() { a.MCP.Close(); pool, _ := a.DB.DB(); _ = pool.Close() }

// Handler godoc
// @Title Wave AI API
// @Version 1.0
// @Description Managed Agents 服务。所有 /v1 接口使用 Bearer Wave API Key 认证； || Managed Agents service. All /v1 endpoints require a Bearer Wave API Key;
// @Description 由 `wave bootstrap` 签发。Authorization 填写完整的 `Bearer <Wave API Key>`。 || issue it with `wave bootstrap`. Set Authorization to the full `Bearer <Wave API Key>` value.
// @Description offset 分页返回 data 和可选 next_offset（省略表示末页）；消息历史使用 next_after，事件列表使用最后一条 sequence 续读。 || Offset pagination returns data and an optional next_offset (omitted on the last page); message history uses next_after, and event lists resume from the last sequence.
// @Description JSON 错误返回 {"type":"error","error":{"type":"api_error","message":"..."},"request_id":"req_..."}。文件传输的 Range 错误返回 text/plain；条件下载和健康检查可返回空响应。 || JSON errors return {"type":"error","error":{"type":"api_error","message":"..."},"request_id":"req_..."}. File-transfer Range errors return text/plain; conditional downloads and health checks may return empty responses.
// @Description JSON 请求拒绝未知字段，非 multipart 请求体上限为 2 MiB。可空字段通过 x-nullable 标记；省略与 null 不等价。 || JSON requests reject unknown fields; non-multipart request bodies are limited to 2 MiB. Nullable fields use x-nullable; omission is not equivalent to null.
// @Description 交互式文档见 /swagger/index.html，OpenAPI spec 见 /swagger/doc.json。 || Interactive documentation: /swagger/index.html. OpenAPI spec: /swagger/doc.json.
// @SecurityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description 填写完整的 Bearer <Wave API Key>，包含 Bearer 前缀和空格。 || Enter the full Bearer <Wave API Key> value, including the Bearer prefix and a space.
func (a *App) Handler() http.Handler {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	_ = r.SetTrustedProxies(nil)
	r.Use(httpx.Observe())
	swaggerUI := ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/swagger/openapi.json"))
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
		swaggerUI(c)
	})
	r.NoRoute(func(c *gin.Context) { httpx.Error(c, apierr.NotFoundErr("route not found")) })
	r.NoMethod(func(c *gin.Context) {
		httpx.Error(c, apierr.New(http.StatusMethodNotAllowed, apierr.InvalidRequest, "method not allowed"))
	})
	r.GET("/health", a.health)
	v := r.Group("/v1", httpx.Authenticate(a.DB))
	v.Use(func(c *gin.Context) {
		if c.ContentType() != "multipart/form-data" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		}
		c.Next()
	})
	agents.Register(v, a.DB)
	environments.Register(v, a.DB)
	execution.Register(v, a.DB)
	files.Register(v, a.DB, a.Blobs)
	memory.Register(v, a.DB)
	vault.Register(v, a.DB, a.Box)
	skills.Register(v, a.DB, a.Blobs)
	deployments.Register(v, a.DB)
	return r
}

// health godoc
// @ID health
// @Summary 健康检查 || Health check
// @Description 无需认证；检查数据库连通性，超时时间 1 秒。所有响应均无响应体。 || No authentication required. Check database connectivity with a one-second timeout. All responses have empty bodies.
// @Tags system
// @Success 204 "数据库可用 || Database available"
// @Failure 503 "数据库不可用 || Database unavailable"
// @Router /health [get]
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
	role := a.Config.Role
	if role == "all" || role == "api" {
		group.Go(func() error { return a.serve(ctx) })
	}
	if role == "all" || role == "scheduler" {
		group.Go(func() error { deployments.RunScheduler(ctx, a.DB); return nil })
	}
	if role == "all" || role == "worker" {
		group.Go(func() error { execution.Maintain(ctx, a.DB); return nil })
		for range a.Config.WorkerConcurrency {
			group.Go(func() error {
				worker := &execution.Worker{
					DB:            a.DB,
					Guard:         a.Guard,
					ContextTokens: a.Config.ContextTokens,
					Model:         modelclient.New(a.Config.ModelBaseURL, a.Config.ModelAPIKey, time.Duration(a.Config.ModelTimeoutSec)*time.Second),
					Lease:         time.Duration(a.Config.LeaseSeconds) * time.Second,
					Execute:       a.execute,
					Prepare:       a.prepare,
					Finish:        a.finish,
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
		IdleTimeout:       120 * time.Second,
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
