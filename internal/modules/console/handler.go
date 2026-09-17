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
