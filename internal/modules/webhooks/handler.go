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

// create godoc
// @ID webhooksCreate
// @Summary 创建 Webhook || Create a webhook
// @Tags webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateRequest true "请求 || Request"
// @Success 200 {object} Subscription
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/webhooks [post]
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

// list godoc
// @ID webhooksList
// @Summary 列出 Webhooks || List webhooks
// @Tags webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/webhooks [get]
func (h handler) list(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	rows := []Subscription{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), p).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// get godoc
// @ID webhooksGet
// @Summary 查看 Webhook || Get a webhook
// @Tags webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Success 200 {object} Subscription
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/webhooks/{id} [get]
func (h handler) get(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	s, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, s)
}

// update godoc
// @ID webhooksUpdate
// @Summary 暂停或恢复 Webhook || Pause or resume a webhook
// @Tags webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param body body UpdateRequest true "请求 || Request"
// @Success 200 {object} Subscription
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/webhooks/{id} [patch]
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

// deliveries godoc
// @ID webhooksDeliveries
// @Summary Webhook 投递记录 || Webhook deliveries
// @Tags webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} DeliveriesResponse
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/webhooks/{id}/deliveries [get]
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

// retry godoc
// @ID webhooksRetry
// @Summary 重试投递 || Retry delivery
// @Tags webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param delivery path string true "资源 ID || Resource ID"
// @Success 200 {object} Delivery
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/webhooks/{id}/deliveries/{delivery}/retry [post]
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
