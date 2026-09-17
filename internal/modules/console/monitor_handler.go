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
