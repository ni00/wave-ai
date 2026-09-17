package webhooks

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/secrets"
)

type handler struct {
	db  *gorm.DB
	box *secrets.Box
}

func Register(r *gin.RouterGroup, db *gorm.DB, box *secrets.Box) {
	h := handler{db, box}
	r.POST("/webhooks", h.create)
	r.GET("/webhooks", h.list)
	r.GET("/webhooks/:id", h.get)
	r.PATCH("/webhooks/:id", h.update)
	r.GET("/webhooks/:id/deliveries", h.deliveries)
	r.POST("/webhooks/:id/deliveries/:delivery/retry", h.retry)
}

// create handles the documented HTTP operation.
func (h handler) create(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var in CreateRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON"))
		return
	}
	s, e := Create(c.Request.Context(), h.db, h.box, p, in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, s)
}

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	rows := []Subscription{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), p).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// get handles the documented HTTP operation.
func (h handler) get(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	s, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, s)
}

// update handles the documented HTTP operation.
func (h handler) update(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var in UpdateRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON"))
		return
	}
	s, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	e = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		s.Paused = *in.Paused
		if e := tx.Model(&s).Update("paused", s.Paused).Error; e != nil {
			return e
		}
		if !s.Paused {
			return tx.Model(&Delivery{}).Where(clause.And(clause.Eq{Column: "subscription_id", Value: s.ID}, clause.Eq{Column: "status", Value: "paused"})).Updates(map[string]any{"status": "pending", "next_at": time.Now()}).Error
		}
		return nil
	})
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, s)
}

// deliveries handles the documented HTTP operation.
func (h handler) deliveries(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	s, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Delivery{}
	if e = h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "subscription_id", Value: s.ID}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// retry handles the documented HTTP operation.
func (h handler) retry(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	s, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	var d Delivery
	e = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if s.Paused {
			return apierr.Conflict("subscription paused")
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(clause.And(clause.Eq{Column: "id", Value: c.Param("delivery")}, clause.Eq{Column: "subscription_id", Value: s.ID})).Take(&d).Error; e != nil {
			return e
		}
		if d.Status != "failed" && d.Status != "paused" {
			return apierr.Conflict("delivery cannot be retried in its current state")
		}
		d.Status = "pending"
		d.Attempts = 0
		d.NextAt = time.Now()
		return tx.Save(&d).Error
	})
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, d)
}
