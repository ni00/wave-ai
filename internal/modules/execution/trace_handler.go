package execution

import (
	"github.com/gin-gonic/gin"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/observe"
)

// trace handles the documented HTTP operation.
func (h handler) trace(c *gin.Context) {
	t, err := TaskTrace(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	s := observe.From(c.Request.Context())
	s.TraceID, s.TaskID, s.SessionID = t.TraceID, t.TaskID, t.SessionID
	c.Request = c.Request.WithContext(observe.With(c.Request.Context(), s))
	c.Header("X-Wave-Trace-ID", t.TraceID)
	c.JSON(200, t)
}

func correlateTask(c *gin.Context, t Task) {
	s := observe.From(c.Request.Context())
	s.TraceID, s.TaskID, s.SessionID = t.RootID, t.ID, t.SessionID
	c.Request = c.Request.WithContext(observe.With(c.Request.Context(), s))
	c.Header("X-Wave-Trace-ID", t.RootID)
}
