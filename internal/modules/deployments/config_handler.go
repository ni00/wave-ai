package deployments

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
	"time"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

// get handles the documented HTTP operation.
func (h handler) get(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	d, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, d)
}

// update handles the documented HTTP operation.
func (h handler) update(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var in UpdateRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON"))
		return
	}
	d, e := Update(c.Request.Context(), h.db, p, c.Param("id"), in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, d)
}

// versions handles the documented HTTP operation.
func (h handler) versions(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	d, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Version{}
	if e = h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "deployment_id", Value: d.ID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "number"}, Desc: true}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// schedule handles the documented HTTP operation.
func (h handler) schedule(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	d, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	out, e := Upcoming(d, time.Now())
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, out)
}
