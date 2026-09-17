package memory

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
	r.DELETE("/memory-stores/:id/entries", h.deleteEntry)
	r.GET("/memory-stores/:id/revisions", h.listRevisions)

	r.POST("/memory-stores", h.createStore)
	r.GET("/memory-stores", h.listStores)
	r.GET("/memory-stores/:id/entries", h.listEntries)
	r.PUT("/memory-stores/:id/entries", h.writeEntry)
	r.GET("/memory-stores/:id/conflicts", h.listConflicts)
}

// deleteEntry handles the documented HTTP operation.
func (h handler) deleteEntry(c *gin.Context) {
	var in DeleteEntryRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	row, e := Delete(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Path, "", in.Version)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// listRevisions handles the documented HTTP operation.
func (h handler) listRevisions(c *gin.Context) {
	if _, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Revision{}
	if e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "store_id", Value: c.Param("id")}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// createStore handles the documented HTTP operation.
func (h handler) createStore(c *gin.Context) {
	var in CreateStoreRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	s, e := Create(c.Request.Context(), h.db, p, in.Name)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, s)
}

// listStores handles the documented HTTP operation.
func (h handler) listStores(c *gin.Context) {
	rows := []Store{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// listEntries handles the documented HTTP operation.
func (h handler) listEntries(c *gin.Context) {
	if _, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Entry{}
	if e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "store_id", Value: c.Param("id")}).Order(clause.OrderByColumn{Column: clause.Column{Name: "path"}}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// writeEntry handles the documented HTTP operation.
func (h handler) writeEntry(c *gin.Context) {
	var in WriteEntryRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	row, e := Write(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Path, in.Content, "", in.Version)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// listConflicts handles the documented HTTP operation.
func (h handler) listConflicts(c *gin.Context) {
	if _, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Conflict{}
	e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "store_id", Value: c.Param("id")}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
