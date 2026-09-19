package execution

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

type handler struct {
	db     *gorm.DB
	signal EventSignaler
}

// Register mounts the documented routes. A non-nil signal lets event streams
// wake on committed events instead of waiting out their poll interval; nil
// keeps streams in pure polling mode (tests, role splits without a listener).
func Register(r *gin.RouterGroup, db *gorm.DB, signal EventSignaler) {
	h := handler{db: db, signal: signal}
	r.GET("/sessions/:id/messages", h.messages)
	r.GET("/sessions/:id/required_actions", h.requiredActions)
	r.GET("/tasks/:id/generations", h.generations)
	r.GET("/tasks/:id/trace", h.trace)
	r.POST("/sessions/:id/archive", h.archiveSession)
	r.POST("/tasks/:id/reconcile", h.reconcile)
	r.POST("/sessions", h.createSession)
	r.GET("/sessions", h.listSessions)
	r.GET("/sessions/:id", h.getSession)
	r.POST("/sessions/:id/tasks", h.createTask)
	r.GET("/sessions/:id/tasks", h.listTasks)
	r.GET("/tasks/:id", h.getTask)
	r.POST("/tasks/:id/inputs", h.addInput)
	r.POST("/tasks/:id/cancel", h.cancelTask)
	r.POST("/tasks/:id/resume", h.resumeTask)
	r.POST("/tasks/:id/agents", h.delegateTask)
	r.GET("/tasks/:id/collaboration", h.collaboration)
	r.GET("/tasks/:id/inputs", h.listInputs)
	r.GET("/tasks/:id/tools", h.listTools)
	r.POST("/tasks/:id/tools/:call/result", h.resolveToolResult)
	r.GET("/sessions/:id/events", h.listEvents)
	r.GET("/sessions/:id/events/stream", h.streamEvents)
}

// messages handles the documented HTTP operation.
func (h handler) messages(c *gin.Context) {
	after, e := cursor(c)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows, e := History(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), c.Query("task_id"), after)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	next := after
	if len(rows) > 0 {
		next = rows[len(rows)-1].Seq
	}
	c.JSON(200, MessagesResponse{Data: rows, NextAfter: next})
}

// requiredActions handles the documented HTTP operation.
func (h handler) requiredActions(c *gin.Context) {
	rows, e := RequiredActions(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, RequiredActionsResponse{Data: rows})
}

// generations handles the documented HTTP operation.
func (h handler) generations(c *gin.Context) {
	var t Task
	if e := h.db.WithContext(c.Request.Context()).Where(eq("id", c.Param("id"))).Take(&t).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	if _, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), t.SessionID); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Generation{}
	if e := h.db.WithContext(c.Request.Context()).Where(eq("task_id", t.ID)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// archiveSession handles the documented HTTP operation.
func (h handler) archiveSession(c *gin.Context) {
	if e := Archive(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}

// reconcile handles the documented HTTP operation.
func (h handler) reconcile(c *gin.Context) {
	var in ReconcileRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	if e := Reconcile(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.ConfirmStopped); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(202)
}

// createSession handles the documented HTTP operation.
func (h handler) createSession(c *gin.Context) {
	var in CreateSessionRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	if e := ValidateSessionResources(c.Request.Context(), h.db, p, in.EnvironmentID, in.FileIDs, in.MemoryIDs, in.VaultIDs); e != nil {
		httpx.Error(c, e)
		return
	}
	s := Session{
		ID:            xid.New("session"),
		OrgID:         p.OrgID,
		OwnerID:       p.PrincipalID,
		Title:         in.Title,
		EnvironmentID: in.EnvironmentID,
		FileIDs:       in.FileIDs,
		MemoryIDs:     in.MemoryIDs,
		VaultIDs:      in.VaultIDs,
	}
	if e := h.db.WithContext(c.Request.Context()).Create(&s).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, s)
}

// listSessions handles the documented HTTP operation.
func (h handler) listSessions(c *gin.Context) {
	rows := []Session{}
	e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// getSession handles the documented HTTP operation.
func (h handler) getSession(c *gin.Context) {
	s, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, s)
}

// createTask handles the documented HTTP operation.
func (h handler) createTask(c *gin.Context) {
	var in TaskRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	t, e := Admit(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.GetHeader("Idempotency-Key"), c.Request.URL.Path, in, func(tx *gorm.DB) (Task, error) {
		return CreateTask(c.Request.Context(), tx, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.AgentID, in.Input, in.Budget)
	})
	if e != nil {
		httpx.Error(c, e)
		return
	}
	correlateTask(c, t)
	c.JSON(202, t)
}

// listTasks handles the documented HTTP operation.
func (h handler) listTasks(c *gin.Context) {
	if _, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Task{}
	e := h.db.WithContext(c.Request.Context()).Where(eq("session_id", c.Param("id"))).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// getTask handles the documented HTTP operation.
func (h handler) getTask(c *gin.Context) {
	var t Task
	db := h.db.WithContext(c.Request.Context())
	p := c.MustGet("principal").(*auth.Principal)
	// Check session ownership in the task read so each poll needs only one
	// query after authentication. The session primary key prevents duplicate rows.
	from := clause.From{Joins: []clause.Join{{
		Type: clause.InnerJoin, Table: clause.Table{Name: "sessions"},
		ON: clause.Where{Exprs: []clause.Expression{clause.Eq{
			Column: clause.Column{Table: "sessions", Name: "id"},
			Value:  clause.Column{Table: "tasks", Name: "session_id"},
		}}},
	}}}
	owned := clause.And(
		clause.Eq{Column: clause.Column{Table: "sessions", Name: "org_id"}, Value: p.OrgID},
		clause.Eq{Column: clause.Column{Table: "sessions", Name: "owner_id"}, Value: p.PrincipalID},
		clause.Eq{Column: clause.Column{Table: "tasks", Name: "id"}, Value: c.Param("id")},
	)
	if e := db.Clauses(from).Omit("messages").Where(owned).Take(&t).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	correlateTask(c, t)
	c.JSON(200, t)
}

// addInput handles the documented HTTP operation.
func (h handler) addInput(c *gin.Context) {
	var in TextRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	if _, e := Admit(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.GetHeader("Idempotency-Key"), c.Request.URL.Path, in, func(tx *gorm.DB) (struct{}, error) {
		return struct{}{}, Steer(c.Request.Context(), tx, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Text)
	}); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(202)
}

// cancelTask handles the documented HTTP operation.
func (h handler) cancelTask(c *gin.Context) {
	if e := Cancel(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(202)
}

// resumeTask handles the documented HTTP operation.
func (h handler) resumeTask(c *gin.Context) {
	var in TextRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	if e := Followup(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Text); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(202)
}

// delegateTask handles the documented HTTP operation.
func (h handler) delegateTask(c *gin.Context) {
	var in TaskRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	t, e := Admit(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.GetHeader("Idempotency-Key"), c.Request.URL.Path, in, func(tx *gorm.DB) (Task, error) {
		return Spawn(c.Request.Context(), tx, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.AgentID, in.Input, in.Budget)
	})
	if e != nil {
		httpx.Error(c, e)
		return
	}
	correlateTask(c, t)
	c.JSON(202, t)
}

// listInputs handles the documented HTTP operation.
func (h handler) listInputs(c *gin.Context) {
	var t Task
	if e := h.db.WithContext(c.Request.Context()).Where(eq("id", c.Param("id"))).Take(&t).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	if _, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), t.SessionID); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Input{}
	if e := h.db.WithContext(c.Request.Context()).Where(eq("task_id", t.ID)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// listTools handles the documented HTTP operation.
func (h handler) listTools(c *gin.Context) {
	var t Task
	if e := h.db.WithContext(c.Request.Context()).Where(eq("id", c.Param("id"))).Take(&t).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	if _, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), t.SessionID); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []ToolCall{}
	if e := h.db.WithContext(c.Request.Context()).Where(eq("task_id", t.ID)).Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// resolveToolResult handles the documented HTTP operation.
func (h handler) resolveToolResult(c *gin.Context) {
	var in ToolResultRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	if (in.Result == nil) == (in.Approve == nil) {
		httpx.Error(c, apierr.Invalid("submit exactly one of result or approve"))
		return
	}
	result := ToolResult{IsError: in.IsError, ErrorCode: in.ErrorCode}
	if in.Result != nil {
		result.Output = *in.Result
	}
	if e := Resolve(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), c.Param("call"), result, in.Approve); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(202)
}

// listEvents handles the documented HTTP operation.
func (h handler) listEvents(c *gin.Context) {
	if _, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	after, e := cursor(c)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows, e := Events(c.Request.Context(), h.db, c.Param("id"), after)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// streamEvents handles the documented HTTP operation.
func (h handler) streamEvents(c *gin.Context) {
	s, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	after, e := cursor(c)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	if after > s.EventSeq {
		httpx.Error(c, apierr.Invalid("cursor is ahead of the event history"))
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	ctl := http.NewResponseController(c.Writer)
	streamPoll := newStreamPoll()
	defer streamPoll.stop()
	var wake <-chan struct{}
	if h.signal != nil {
		// Session-scoped subscription: wake only for this session's
		// committed events, canceled with the request.
		wake = h.signal.Subscribe(c.Request.Context(), s.ID)
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		rows, e := Events(c.Request.Context(), h.db, s.ID, after)
		if e != nil {
			return
		}
		for _, ev := range rows {
			raw, e := json.Marshal(ev)
			if e != nil {
				return
			}
			_ = ctl.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, e = fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, raw); e != nil {
				return
			}
			after = ev.Seq
		}
		if e = ctl.Flush(); e != nil {
			return
		}
		if len(rows) == 200 {
			continue
		}
		// Adaptive cadence: an active stream polls at the base rate; each
		// empty poll doubles the interval up to the ceiling so an idle
		// stream stops re-querying at full rate. A wake signal (or a
		// notification-driven fetch) restores the active rate immediately.
		if len(rows) > 0 {
			streamPoll.active()
		} else {
			streamPoll.idle()
		}
		streamPoll.arm()
		select {
		case <-c.Request.Context().Done():
			return
		case <-wake:
			// Events were committed for this session; fetch them now rather
			// than waiting out the (possibly idle-grown) interval.
		case <-streamPoll.channel():
		case <-ping.C:
			_ = ctl.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, e = fmt.Fprint(c.Writer, ": ping\n\n"); e != nil {
				return
			}
		}
	}
}

// streamPoll sequences the SSE fallback polls: 250 ms while the session is
// producing events, doubling per empty poll up to 1 s. The notification wake
// path bypasses it entirely, so growth only ever affects quiet streams.
type streamPoll struct {
	base, ceiling time.Duration
	level         time.Duration
	timer         *time.Timer
}

func newStreamPoll() *streamPoll {
	return &streamPoll{base: 250 * time.Millisecond, ceiling: time.Second, level: 250 * time.Millisecond, timer: time.NewTimer(time.Nanosecond)}
}

func (p *streamPoll) active() { p.level = p.base }
func (p *streamPoll) idle()   { p.level = min(p.level*2, p.ceiling) }

// arm restarts the timer for the current level, draining a pending fire so a
// stale tick can never leak into the next wait.
func (p *streamPoll) arm() {
	if !p.timer.Stop() {
		select {
		case <-p.timer.C:
		default:
		}
	}
	p.timer.Reset(p.level)
}

func (p *streamPoll) channel() <-chan time.Time { return p.timer.C }
func (p *streamPoll) stop()                     { p.timer.Stop() }

func cursor(c *gin.Context) (int64, error) {
	raw := c.Query("after")
	if raw == "" {
		raw = c.GetHeader("Last-Event-ID")
	}
	if raw == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 0 {
		return 0, apierr.Invalid("after/Last-Event-ID must be a nonnegative event sequence")
	}
	return n, nil
}

// collaboration handles the documented HTTP operation.
func (h handler) collaboration(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var t Task
	e := h.db.WithContext(c.Request.Context()).Where(eq("id", c.Param("id"))).Take(&t).Error
	if e == nil {
		_, e = session(c.Request.Context(), h.db, p, t.SessionID)
	}
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Task{}
	if e = h.db.WithContext(c.Request.Context()).Omit("messages").Where(eq("root_id", t.RootID)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
