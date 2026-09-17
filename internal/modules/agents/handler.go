package agents

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
	r.POST("/agents", h.create)
	r.GET("/agents", h.list)
	r.GET("/agents/:id", h.get)
	r.PUT("/agents/:id", h.update)
	r.POST("/agents/:id/archive", h.archive)
	r.GET("/agents/:id/versions", h.versions)
}

// create handles the documented HTTP operation.
func (h handler) create(c *gin.Context) {
	var in Config
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	a, e := Create(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, a)
}

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	rows := []Agent{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// get handles the documented HTTP operation.
func (h handler) get(c *gin.Context) {
	a, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, a)
}

// update handles the documented HTTP operation.
func (h handler) update(c *gin.Context) {
	var in UpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	a, e := Update(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Version, in.Config)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, a)
}

// archive handles the documented HTTP operation.
func (h handler) archive(c *gin.Context) {
	a, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e == nil {
		e = h.db.WithContext(c.Request.Context()).Model(&a).Update("archived", true).Error
	}
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}

// versions handles the documented HTTP operation.
func (h handler) versions(c *gin.Context) {
	a, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Version{}
	e = h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "agent_id", Value: a.ID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "number"}, Desc: true}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
