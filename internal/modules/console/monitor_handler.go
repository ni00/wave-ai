package console

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
	"time"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/telemetry"
)

// @ID consoleLog
// @Summary 读取结构化日志 || Read a structured log
// @Tags console
// @Produce json
// @Security BearerAuth
// @Param id path string true "日志 ID || Log ID"
// @Success 200 {object} observe.Log
// @Failure 401 {object} apierr.Envelope
// @Failure 403 {object} apierr.Envelope
// @Failure 404 {object} apierr.Envelope
// @Failure 500 {object} apierr.Envelope
// @Router /v1/console/logs/{id} [get]
func (h handler) log(c *gin.Context) {
	ctx, cancel := contextTimeout(c)
	defer cancel()
	var row observe.Log
	if err := auth.Owned(h.db.WithContext(ctx), c.MustGet("principal").(*auth.Principal)).Where(clause.Eq{Column: "id", Value: c.Param("id")}).Take(&row).Error; err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, row)
}

// @ID consoleMetrics
// @Summary 查询监控指标 || Query monitoring metrics
// @Description 仅查询聚合时间桶。最多 30 天；时间向外对齐，响应返回实际范围。延迟分位数为 2% 直方图估计；未知值为 null。 || Reads aggregate buckets only. Maximum 30 days; range aligned outwards and returned. Latency percentiles use a 2% histogram; unknown values are null.
// @Tags console
// @Produce json
// @Security BearerAuth
// @Param from query string false "开始时间 RFC3339（默认最近 1 小时） || Start RFC3339 (defaults to last hour)"
// @Param to query string false "结束时间 RFC3339（默认现在） || End RFC3339 (defaults to now)"
// @Success 200 {object} telemetry.Metrics
// @Failure 400 {object} apierr.Envelope
// @Failure 401 {object} apierr.Envelope
// @Failure 403 {object} apierr.Envelope
// @Failure 500 {object} apierr.Envelope
// @Router /v1/console/metrics [get]
func (h handler) metrics(c *gin.Context) {
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	for key, target := range map[string]*time.Time{"from": &from, "to": &to} {
		if raw := c.Query(key); raw != "" {
			at, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				httpx.Error(c, apierr.Invalid("metrics range requires RFC3339"))
				return
			}
			*target = at
		}
	}
	if !from.Before(to) || to.Sub(from) > 30*24*time.Hour || from.Before(time.Now().Add(-30*24*time.Hour-time.Minute)) || to.After(time.Now().Add(time.Minute)) {
		httpx.Error(c, apierr.Invalid("metrics range must be within the last 30 days"))
		return
	}
	ctx, cancel := contextTimeout(c)
	defer cancel()
	out, err := telemetry.Query(ctx, h.db, c.MustGet("principal").(*auth.Principal), from, to)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, out)
}
