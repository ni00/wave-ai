package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/httpx"
)

type handler struct {
	db    *gorm.DB
	blobs *blobstore.Store
}

func Register(r *gin.RouterGroup, db *gorm.DB, blobs *blobstore.Store) {
	h := handler{db, blobs}
	r.GET("/console/resources/:kind", h.browse)
	r.GET("/console/resources/:kind/:id", h.resource)
	r.GET("/console/events/:session/:sequence", h.event)
	r.GET("/console/logs/:id", h.log)
	r.GET("/console/metrics", h.metrics)
	r.POST("/console/benchmarks", h.importBench)
}

// @ID consoleBrowse
// @Summary 浏览后台资源 || Browse console resources
// @Tags console
// @Accept json
// @Produce json
// @Security BearerAuth
// @Description 按当前所有者过滤。列表只含元数据，以不透明游标倒序分页；logs 是结构化应用日志，events 是执行事件（排除 token delta）。 || Owner-scoped metadata only, with descending opaque cursor pagination. Logs are structured application logs; events are execution events excluding token deltas.
// @Param kind path string true "资源类型 || Resource kind" Enums(traces,logs,events,environments,sandboxes,skills,agents,deployments,vaults,webhooks,files,memory,sessions,tasks,benchmarks)
// @Param q query string false "名称或 ID（最多 128 字节） || Name or ID (maximum 128 bytes)"
// @Param state query string false "状态；日志级别；事件类型 || State, log level, or event type"
// @Param module query string false "日志模块 || Log module"
// @Param trace_id query string false "链路 ID || Trace ID"
// @Param span_id query string false "Span ID"
// @Param time_field query string false "时间字段 || Time field" Enums(created_at,finished_at)
// @Param session_id query string false "会话 ID || Session ID"
// @Param agent_id query string false "Agent ID"
// @Param environment_id query string false "环境 ID || Environment ID"
// @Param skill_id query string false "关联技能 ID || Referenced skill ID"
// @Param task_id query string false "任务 ID || Task ID"
// @Param before query string false "早于 RFC3339 时间 || Before RFC3339 timestamp"
// @Param after query string false "不早于 RFC3339 时间 || At or after RFC3339 timestamp"
// @Param cursor query string false "上一页 next_cursor || Previous next_cursor"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Success 200 {object} List
// @Failure 400 {object} apierr.Envelope "参数或报告无效 || Invalid parameters or report"
// @Failure 401 {object} apierr.Envelope "身份验证失败 || Authentication required"
// @Failure 403 {object} apierr.Envelope "权限不足 || Insufficient scope"
// @Failure 404 {object} apierr.Envelope "资源不存在 || Resource not found"
// @Failure 500 {object} apierr.Envelope "内部错误 || Internal error"
// @Router /v1/console/resources/{kind} [get]
func (h handler) browse(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil {
		httpx.Error(c, apierr.Invalid("invalid limit"))
		return
	}
	ctx, cancel := contextTimeout(c)
	defer cancel()
	out, err := Browse(ctx, h.db, c.MustGet("principal").(*auth.Principal), Query{Kind: c.Param("kind"), AgentID: c.Query("agent_id"), EnvironmentID: c.Query("environment_id"), SkillID: c.Query("skill_id"), Module: c.Query("module"), TraceID: c.Query("trace_id"), SpanID: c.Query("span_id"), TimeField: c.Query("time_field"), Search: c.Query("q"), State: c.Query("state"), SessionID: c.Query("session_id"), TaskID: c.Query("task_id"), Before: c.Query("before"), After: c.Query("after"), Cursor: c.Query("cursor"), Limit: limit})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, out)
}

// @ID consoleEvent
// @Summary 读取执行事件 || Read an execution event
// @Tags console
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param session path string true "会话 ID || Session ID"
// @Param sequence path int true "事件序号 || Event sequence" format(int64)
// @Success 200 {object} execution.Event
// @Failure 400 {object} apierr.Envelope "参数或报告无效 || Invalid parameters or report"
// @Failure 401 {object} apierr.Envelope "身份验证失败 || Authentication required"
// @Failure 403 {object} apierr.Envelope "权限不足 || Insufficient scope"
// @Failure 404 {object} apierr.Envelope "资源不存在 || Resource not found"
// @Failure 500 {object} apierr.Envelope "内部错误 || Internal error"
// @Router /v1/console/events/{session}/{sequence} [get]
func (h handler) event(c *gin.Context) {
	seq, err := strconv.ParseInt(c.Param("sequence"), 10, 64)
	if err != nil || seq < 1 {
		httpx.Error(c, apierr.Invalid("invalid sequence"))
		return
	}
	ctx, cancel := contextTimeout(c)
	defer cancel()
	var session execution.Session
	if err = auth.Owned(h.db.WithContext(ctx), c.MustGet("principal").(*auth.Principal)).Select("id").Where(clause.Eq{Column: "id", Value: c.Param("session")}).Take(&session).Error; err != nil {
		httpx.Error(c, err)
		return
	}
	var row execution.Event
	if err = h.db.WithContext(ctx).Where(clause.And(clause.Eq{Column: "session_id", Value: session.ID}, clause.Eq{Column: "seq", Value: seq})).Take(&row).Error; err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, row)
}

// @ID consoleImportBench
// @Summary 导入压测报告 || Import a benchmark report
// @Tags console
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "Wave bench v2 JSON 报告，最多 16 MiB || Wave bench v2 JSON report, maximum 16 MiB"
// @Success 201 {object} files.File
// @Failure 400 {object} apierr.Envelope "参数或报告无效 || Invalid parameters or report"
// @Failure 401 {object} apierr.Envelope "身份验证失败 || Authentication required"
// @Failure 403 {object} apierr.Envelope "权限不足 || Insufficient scope"
// @Failure 404 {object} apierr.Envelope "资源不存在 || Resource not found"
// @Failure 500 {object} apierr.Envelope "内部错误 || Internal error"
// @Router /v1/console/benchmarks [post]
func (h handler) importBench(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (16<<20)+(64<<10))
	header, err := c.FormFile("file")
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err != nil {
		httpx.Error(c, apierr.Invalid("report upload required (maximum 16 MiB)"))
		return
	}
	f, err := header.Open()
	if err != nil {
		httpx.Error(c, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	if err = ValidateBench(data); err != nil {
		httpx.Error(c, err)
		return
	}
	var row files.File
	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = files.Put(c.Request.Context(), tx, h.blobs, c.MustGet("principal").(*auth.Principal), "", header.Filename, data)
		if e != nil {
			return e
		}
		row.MIME = BenchMIME
		return tx.Model(&row).Update("mime", BenchMIME).Error
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(201, row)
}

// ValidateBench accepts only finite, structurally valid native v2 reports.
func ValidateBench(data []byte) error {
	if len(data) > 16<<20 {
		return apierr.Invalid("report exceeds 16 MiB")
	}
	var report struct {
		Version   int       `json:"version"`
		RunID     string    `json:"run_id"`
		Kind      string    `json:"kind"`
		StartedAt time.Time `json:"started_at"`
		Phases    []struct {
			Workers   int     `json:"workers"`
			Attempted int     `json:"attempted"`
			Succeeded int     `json:"succeeded"`
			Failed    int     `json:"failed"`
			Elapsed   float64 `json:"elapsed_seconds"`
		} `json:"phases"`
	}
	if json.Unmarshal(data, &report) != nil || report.Version != 2 || report.RunID == "" || len(report.RunID) > 128 || report.StartedAt.IsZero() || (report.Kind != "live" && report.Kind != "synthetic") || len(report.Phases) < 1 || len(report.Phases) > 200 {
		return apierr.Invalid("expected a Wave bench v2 live or synthetic report")
	}
	for _, p := range report.Phases {
		if (p.Workers < 0 || (report.Kind == "synthetic" && p.Workers == 0)) || p.Attempted < 0 || p.Succeeded < 0 || p.Failed < 0 || p.Elapsed < 0 || p.Succeeded > p.Attempted || p.Failed > p.Attempted-p.Succeeded {
			return apierr.Invalid("invalid benchmark phase")
		}
	}
	return nil
}

func contextTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}
