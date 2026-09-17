package deployments

import (
	"wave-ai.local/wave/internal/platform/apierr"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
	r.POST("/deployments", h.create)
	r.GET("/deployments", h.list)
	r.GET("/deployments/:id", h.get)
	r.PUT("/deployments/:id", h.update)
	r.GET("/deployments/:id/versions", h.versions)
	r.GET("/deployments/:id/schedule", h.schedule)
	r.POST("/deployments/:id/run", h.run)
	r.PATCH("/deployments/:id", h.pause)
	r.GET("/deployments/:id/runs", h.runs)
}

// create handles the documented HTTP operation.
func (h handler) create(c *gin.Context) {
	var in CreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	d, e := Create(c.Request.Context(), h.db, p, in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, d)
}

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	rows := []Deployment{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// run handles the documented HTTP operation.
func (h handler) run(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	row, e := execution.Admit(c.Request.Context(), h.db, p, c.GetHeader("Idempotency-Key"), "deployment.run", c.Param("id"), func(tx *gorm.DB) (Run, error) { return Fire(c.Request.Context(), tx, p, c.Param("id"), false) })
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(202, row)
}

// pause handles the documented HTTP operation.
func (h handler) pause(c *gin.Context) {
	var in PauseRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	d, e := Pause(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), *in.Paused)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, d)
}

// runs handles the documented HTTP operation.
func (h handler) runs(c *gin.Context) {
	var d Deployment
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Where(clause.Eq{Column: "id", Value: c.Param("id")}).Take(&d).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Run{}
	if e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "deployment_id", Value: d.ID}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
