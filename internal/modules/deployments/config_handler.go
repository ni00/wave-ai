package deployments

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
	"time"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

// get godoc
// @ID deploymentsGet
// @Summary 查看部署 || Get a deployment
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Success 200 {object} Deployment
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/deployments/{id} [get]
func (h handler) get(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	d, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, d)
}

// update godoc
// @ID deploymentsUpdate
// @Summary 更新部署配置 || Update deployment configuration
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param body body UpdateRequest true "请求 || Request"
// @Success 200 {object} Deployment
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/deployments/{id} [put]
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

// versions godoc
// @ID deploymentsVersions
// @Summary 部署版本 || Deployment versions
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} VersionsResponse
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/deployments/{id}/versions [get]
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

// schedule godoc
// @ID deploymentsSchedule
// @Summary 未来触发时间 || Upcoming occurrences
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Success 200 {object} ScheduleResponse
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/deployments/{id}/schedule [get]
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
