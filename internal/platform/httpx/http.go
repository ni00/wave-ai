// Package httpx contains only HTTP boundary helpers; business code never imports it.
package httpx

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"wave-ai.local/wave/internal/platform/observe"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

func Error(c *gin.Context, err error) {
	var e *apierr.Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		e = apierr.NotFoundErr("resource not found")
	} else if errors.Is(err, gorm.ErrDuplicatedKey) {
		e = apierr.New(409, apierr.InvalidRequest, "resource already exists")
	} else if !errors.As(err, &e) {
		observe.Logger(c.Request.Context()).Error("http.request_failed", "error_kind", observe.ErrorKind(err))
		e = apierr.New(500, apierr.API, "internal error")
	}
	c.AbortWithStatusJSON(e.Status, gin.H{"type": "error", "error": gin.H{"type": e.Type, "message": e.Message}, "request_id": c.GetString("request_id")})
}
func Observe() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		rid := apierr.NewRequestID()
		c.Set("request_id", rid)
		c.Header("X-Request-ID", rid)
		c.Request = c.Request.WithContext(observe.With(c.Request.Context(), observe.Scope{RequestID: rid}))
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				observe.Logger(c.Request.Context()).Error("http.panic")
				if c.Writer.Written() {
					panic(http.ErrAbortHandler)
				}
				Error(c, apierr.New(500, apierr.API, "internal error"))
			}
			observe.Logger(c.Request.Context()).Info("http.request", "method", c.Request.Method, "route", c.FullPath(), "status", c.Writer.Status(), "duration_ms", float64(time.Since(start))/float64(time.Millisecond))
		}()
		c.Next()
	}
}
func Authenticate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		hdr := c.GetHeader("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") {
			Error(c, apierr.Unauthorized("missing bearer key"))
			return
		}
		p, err := auth.Authenticate(c.Request.Context(), db, strings.TrimPrefix(hdr, "Bearer "))
		if err != nil {
			Error(c, err)
			return
		}
		if p.Scope != auth.ScopeAPI {
			Error(c, apierr.Forbidden("API scope required"))
			return
		}
		c.Set("principal", p)
		c.Next()
	}
}

func init() { gin.EnableJsonDecoderDisallowUnknownFields() }
