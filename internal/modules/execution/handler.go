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

	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
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
	r.GET("/tasks/:id/inputs", h.listInputs)
	r.GET("/tasks/:id/tools", h.listTools)
	r.POST("/tasks/:id/tools/:call/result", h.resolveToolResult)
	r.GET("/sessions/:id/events", h.listEvents)
	r.GET("/sessions/:id/events/stream", h.streamEvents)
}

// messages godoc
// @ID executionMessages
// @Summary 会话消息历史 || Session message history
// @Description 返回会话的不可变消息历史，按会话序号升序分页，每页最多 200 条，通过 next_after 继续读取。可用 task_id 过滤单个任务的消息。模型预览和事件不在此历史中，只有确认完成的输出才会提交。 || Return immutable session message history, paginated by session sequence ascending, up to 200 messages per page. Continue with next_after and filter a task with task_id. Model previews and events are excluded; only confirmed completed output is committed.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Param after query int64 false "从该会话消息序号之后读取，默认 0 || Read after this session message sequence; defaults to 0" minimum(0) default(0)
// @Param task_id query string false "按任务 ID 过滤 || Filter by task ID"
// @Param Last-Event-ID header string false "消息序号的备用游标；after 非空时优先使用 after || Fallback message-sequence cursor; a nonempty after takes precedence"
// @Success 200 {object} MessagesResponse
// @Failure 400 {object} apierr.Envelope "after 序号不合法 || Invalid after sequence"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Router /v1/sessions/{id}/messages [get]
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

// requiredActions godoc
// @ID executionRequiredActions
// @Summary 待处理的用户动作 || Required user actions
// @Description 返回会话中未结束任务当前需要的动作：approve_tool（审批工具）、submit_tool_result（自定义工具返回结果）、confirm_tool_outcome（确认外部执行结果）、reconcile_task（环境准备或收尾结果未知，需先确认外部操作已停止再调用 reconcile）。该列表从当前任务与工具状态实时计算，重连后可直接查询。 || Return actions currently required by unfinished tasks in the session: approve_tool (tool approval), submit_tool_result (custom-tool result), confirm_tool_outcome (confirm an external execution outcome), and reconcile_task (unknown environment preparation or finalization outcome; confirm external operations have stopped before calling reconcile). The list is computed from current task and tool states and can be queried after reconnecting.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Success 200 {object} RequiredActionsResponse
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Router /v1/sessions/{id}/required_actions [get]
func (h handler) requiredActions(c *gin.Context) {
	rows, e := RequiredActions(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, RequiredActionsResponse{Data: rows})
}

// generations godoc
// @ID executionGenerations
// @Summary 任务模型调用记录 || Task model-call records
// @Description 按任务返回每次模型调用的记录，包括压缩与重试，涵盖耗时、首个增量时间与供应商 token 用量。缺失的用量保持 null，中断调用标记 interrupted，不按零消耗计算。 || Return model-call records for the task, including compaction and retries, with duration, first-delta latency and provider token usage. Missing usage remains null; interrupted calls are marked interrupted and are not counted as zero usage.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} GenerationsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Router /v1/tasks/{id}/generations [get]
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

// archiveSession godoc
// @ID executionArchiveSession
// @Summary 归档会话 || Archive a session
// @Description 将会话标记为 archived。会话存在未结束任务或活动根任务时拒绝归档。 || Mark the session as archived. Archiving is rejected if unfinished tasks or an active root task remain.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Success 204 "已归档 || Archived"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Failure 409 {object} apierr.Envelope "会话仍有未结束任务 || Session still has unfinished tasks"
// @Router /v1/sessions/{id}/archive [post]
func (h handler) archiveSession(c *gin.Context) {
	if e := Archive(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}

// reconcile godoc
// @ID executionReconcile
// @Summary 确认任务恢复 || Reconcile a task
// @Description 任务状态未知（unknown）时，操作者显式确认外部操作已停止（confirm_stopped=true）后，把任务重新排队。请求体必须显式确认；任务仍有未决工具结果时须先逐个提交结果。 || Requeue an unknown task after the operator explicitly confirms that external operations have stopped (confirm_stopped=true). The request body must include this confirmation; submit each unresolved tool result first.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Param body body ReconcileRequest true "确认外部操作已停止 || Confirm that external operations have stopped"
// @Success 202 "已接受，任务重新排队 || Accepted; task requeued"
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或未显式确认 || Invalid JSON body or missing explicit confirmation"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Failure 409 {object} apierr.Envelope "任务不是未知状态，或仍有未决的工具结果 || Task is not unknown, or tool results are still unresolved"
// @Router /v1/tasks/{id}/reconcile [post]
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

// createSession godoc
// @ID executionCreateSession
// @Summary 创建会话 || Create a session
// @Description 创建一个执行会话。可选绑定环境，以及挂载文件（最多 100 个）和记忆库（最多 20 个）；挂载资源必须提供 environment_id，且环境不能已归档。 || Create an execution session, optionally binding an environment and mounting files (up to 100) and memory stores (up to 20). Mounting resources requires environment_id, and the environment must not be archived.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateSessionRequest true "会话标题、环境与挂载资源 || Session title, environment and mounted resources"
// @Success 201 {object} Session
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON、环境已归档、挂载资源缺少环境或数量超限 || Invalid JSON body, archived environment, mounted resources without an environment, or exceeded resource-count limits"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "环境、文件或记忆库不存在 || Environment, file or memory store not found"
// @Router /v1/sessions [post]
func (h handler) createSession(c *gin.Context) {
	var in CreateSessionRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	if in.EnvironmentID != "" {
		env, e := environments.Get(c.Request.Context(), h.db, p, in.EnvironmentID)
		if e != nil {
			httpx.Error(c, e)
			return
		}
		if env.Archived {
			httpx.Error(c, apierr.Invalid("environment archived"))
			return
		}
	}
	if (len(in.FileIDs) > 0 || len(in.MemoryIDs) > 0) && in.EnvironmentID == "" {
		httpx.Error(c, apierr.Invalid("mounted resources require an environment"))
		return
	}
	if len(in.FileIDs) > 100 || len(in.MemoryIDs) > 20 {
		httpx.Error(c, apierr.Invalid("too many mounted resources"))
		return
	}
	for _, id := range in.FileIDs {
		if _, e := files.Get(c.Request.Context(), h.db, p, id); e != nil {
			httpx.Error(c, e)
			return
		}
	}
	for _, id := range in.MemoryIDs {
		if _, e := memory.Get(c.Request.Context(), h.db, p, id); e != nil {
			httpx.Error(c, e)
			return
		}
	}
	s := Session{
		ID:            xid.New("session"),
		OrgID:         p.OrgID,
		OwnerID:       p.PrincipalID,
		Title:         in.Title,
		EnvironmentID: in.EnvironmentID,
		FileIDs:       in.FileIDs,
		MemoryIDs:     in.MemoryIDs,
	}
	if e := h.db.WithContext(c.Request.Context()).Create(&s).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, s)
}

// listSessions godoc
// @ID executionListSessions
// @Summary 列出会话 || List sessions
// @Description 分页返回当前所有者可见的会话，按 id 排序。 || Return sessions visible to the current owner, paginated and ordered by id.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} SessionsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/sessions [get]
func (h handler) listSessions(c *gin.Context) {
	rows := []Session{}
	e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// getSession godoc
// @ID executionGetSession
// @Summary 获取会话 || Get a session
// @Description 按 ID 返回单个会话。 || Return a session by ID.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Success 200 {object} Session
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Router /v1/sessions/{id} [get]
func (h handler) getSession(c *gin.Context) {
	s, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, s)
}

// createTask godoc
// @ID executionCreateTask
// @Summary 创建任务 || Create a task
// @Description 在会话中按 agent_id 创建根任务并提交初始输入，任务进入 queued 状态。快照固定 Agent 配置与专家名册；预算缺省值自动补齐。会话或 Agent 已归档、技能缺少环境、凭据目标不匹配等会拒绝创建。 || Create a root task in the session using agent_id and initial input; its state starts as queued. The snapshot fixes the Agent configuration and expert roster, and budget defaults are filled in. Creation is rejected for archived sessions or Agents, skills without an environment, mismatched credential targets, and other invalid resources.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Param body body TaskRequest true "Agent ID、输入文本与预算 || Agent ID, input text and budget"
// @Param Idempotency-Key header string false "最长 128 字节；作用域为当前身份和请求路径。同键同请求体返回首次结果，换请求体返回 409 || At most 128 bytes; scoped to the current identity and request path. The same key and body return the original result; a different body returns 409" maxlength(128)
// @Success 202 {object} Task
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON、缺少输入或资源校验失败 || Invalid JSON body, missing input or resource validation failed"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话、Agent、专家、技能或凭据不存在 || Session, Agent, expert, skill or credential not found"
// @Failure 409 {object} apierr.Envelope "Idempotency-Key 已与其他请求体一起使用 || Idempotency-Key has already been used with a different request body"
// @Router /v1/sessions/{id}/tasks [post]
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

// listTasks godoc
// @ID executionListTasks
// @Summary 列出会话任务 || List session tasks
// @Description 分页返回会话内的全部任务（含协调者与子任务），按 id 排序。 || Return all tasks in the session, including coordinator and child tasks, paginated and ordered by id.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} TasksResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Router /v1/sessions/{id}/tasks [get]
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

// getTask godoc
// @ID executionGetTask
// @Summary 获取任务 || Get a task
// @Description 按 ID 返回单个任务，包括状态、预算与用量、协调快照与结果。 || Return a task by ID, including its state, budget and usage, coordination snapshot and result.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Success 200 {object} Task
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Router /v1/tasks/{id} [get]
func (h handler) getTask(c *gin.Context) {
	var t Task
	if e := h.db.WithContext(c.Request.Context()).Where(eq("id", c.Param("id"))).Take(&t).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	if _, e := session(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), t.SessionID); e != nil {
		httpx.Error(c, e)
		return
	}
	correlateTask(c, t)
	c.JSON(200, t)
}

// addInput godoc
// @ID executionAddInput
// @Summary 任务转向输入 || Steer a task with additional input
// @Description 向运行中的任务追加用户输入。任务在等待输入或等待工具时会被重新排队；已终态、已请求取消或正在收尾的任务拒绝接收。 || Append user input to a running task. Tasks waiting for input or tools are requeued; terminal tasks, tasks with cancellation requested, and tasks being finalized reject input.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Param body body TextRequest true "输入文本 || Input text"
// @Param Idempotency-Key header string false "最长 128 字节；作用域为当前身份和请求路径。同键同请求体返回首次结果，换请求体返回 409 || At most 128 bytes; scoped to the current identity and request path. The same key and body return the original result; a different body returns 409" maxlength(128)
// @Success 202 "已接受 || Accepted"
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或文本为空 || Invalid JSON body or empty text"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Failure 409 {object} apierr.Envelope "任务不再接受输入，或 Idempotency-Key 已与其他请求体一起使用 || Task no longer accepts input, or Idempotency-Key has been used with a different request body"
// @Router /v1/tasks/{id}/inputs [post]
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

// cancelTask godoc
// @ID executionCancelTask
// @Summary 取消任务 || Cancel a task
// @Description 请求取消任务；根任务会级联标记全部未结束子任务。取消请求与最终停止分开记录，终态在收尾完成后发布。 || Request task cancellation; root tasks also mark all unfinished child tasks. Cancellation requests and final stoppage are recorded separately; the terminal state is published after finalization.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Success 202 "已接受 || Accepted"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Router /v1/tasks/{id}/cancel [post]
func (h handler) cancelTask(c *gin.Context) {
	if e := Cancel(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(202)
}

// resumeTask godoc
// @ID executionResumeTask
// @Summary 恢复已完成的子任务 || Resume a terminal child task
// @Description 在协调者仍持有会话时，用新输入恢复一个已终态的子任务；上下文与累计预算保持挂载在同一子任务上。协调者已终态或正在收尾时拒绝恢复。 || Resume a terminal child task with new input while its coordinator still holds the session. Context and cumulative budget remain attached to the same child task. Resuming is rejected if the coordinator is terminal or being finalized.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Param body body TextRequest true "续跑输入文本 || Input text for resuming"
// @Success 202 "已接受 || Accepted"
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON、文本为空、不是已完成的子任务或协调者不再接受协作 || Invalid JSON body, empty text, task not a terminal child task, or coordinator no longer accepting collaboration"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Router /v1/tasks/{id}/resume [post]
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

// delegateTask godoc
// @ID executionDelegateTask
// @Summary 委派子任务 || Delegate a child task
// @Description 由父任务向其固定的专家名册中的 Agent 委派子任务，子预算只能收窄父预算。父任务自身是子任务、已终态、已请求取消或正在收尾时不能委派；累计代理预算（max_agents）耗尽时同样拒绝。 || Delegate a child task to an Agent in the parent's fixed expert roster. Child budgets may only narrow the parent budget. Delegation is rejected if the parent is itself a child task, terminal, cancellation-requested or being finalized, or if its cumulative agent budget (max_agents) is exhausted.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "父任务 ID || Parent task ID"
// @Param body body TaskRequest true "专家 Agent ID、输入文本与预算 || Expert Agent ID, input text and budget"
// @Param Idempotency-Key header string false "最长 128 字节；作用域为当前身份和请求路径。同键同请求体返回首次结果，换请求体返回 409 || At most 128 bytes; scoped to the current identity and request path. The same key and body return the original result; a different body returns 409" maxlength(128)
// @Success 202 {object} Task
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON、Agent 不在专家名册或缺少输入 || Invalid JSON body, Agent not in the expert roster or missing input"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "父任务不存在或不在当前所有者范围内 || Parent task not found or outside the current owner's scope"
// @Failure 409 {object} apierr.Envelope "父任务不能委派、代理预算耗尽，或 Idempotency-Key 已与其他请求体一起使用 || Parent task cannot delegate, the agent budget is exhausted, or Idempotency-Key was used with a different body"
// @Router /v1/tasks/{id}/agents [post]
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

// listInputs godoc
// @ID executionListInputs
// @Summary 列出任务输入 || List task inputs
// @Description 按提交顺序返回任务收到的全部输入（用户输入、委派输入等）。 || Return all inputs received by the task, including user and delegation inputs, in submission order.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} InputsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Router /v1/tasks/{id}/inputs [get]
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

// listTools godoc
// @ID executionListTools
// @Summary 列出任务工具调用 || List task tool calls
// @Description 按创建时间返回任务的工具调用，包括参数、状态（ready、approval、custom、running、waiting_children、unknown、completed）、结果与起止时间。 || Return task tool calls by creation time, including arguments, status (ready, approval, custom, running, waiting_children, unknown, completed), results, and start and finish times.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Success 200 {object} ToolCallsResponse
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Router /v1/tasks/{id}/tools [get]
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

// resolveToolResult godoc
// @ID executionResolveToolResult
// @Summary 提交工具审批或结果 || Submit tool approval or result
// @Description 批准示例：`{"approve":true}`；拒绝示例：`{"approve":false}`。 || Approval example: `{"approve":true}`; rejection example: `{"approve":false}`.
// @Description 成功结果示例：`{"result":"tool response","is_error":false}`；失败结果示例：`{"result":"confirmed failure","is_error":true,"error_code":"remote_failure"}`。 || Success example: `{"result":"tool response","is_error":false}`; failure example: `{"result":"confirmed failure","is_error":true,"error_code":"remote_failure"}`.
// @Description 请选用上述示例之一；Swagger 2.0 无法表达字段互斥，不要同时发送自动生成示例中的所有字段。 || Choose one example above. Swagger 2.0 cannot express mutually exclusive fields; do not send all fields from its automatically generated example together.
// @Description 提交以下三种互斥形式之一：审批 {"approve": true|false}；确认结果 {"result": "...", "is_error": false}；明确失败 {"result": "...", "is_error": true, "error_code": "..."}。审批与结果不能混在一个请求中，error_code 要求 is_error=true，空字符串也是有效结果。相同审批或相同完整结果可重复提交（包括任务完成后）；更改已有决定返回 409。任务等待中会因此重新排队。 || Submit exactly one of three forms: approval {"approve": true|false}; confirmed result {"result": "...", "is_error": false}; confirmed failure {"result": "...", "is_error": true, "error_code": "..."}. Do not combine approval and result; error_code requires is_error=true, and an empty string is a valid result. Identical approvals or complete results may be resubmitted, including after task completion; changing an existing decision returns 409. Waiting tasks are requeued.
// @Tags execution
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Param call path string true "工具调用 ID || Tool-call ID"
// @Param body body ToolResultRequest true "审批或工具结果（三选一） || Approval or tool result (choose one of three forms)"
// @Success 202 "已接受 || Accepted"
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON、同时提交 result 与 approve 或 error_code 缺少 is_error || Invalid JSON body, both result and approve submitted, or error_code without is_error"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "任务或工具调用不存在 || Task or tool call not found"
// @Failure 409 {object} apierr.Envelope "工具不在等待审批或结果状态，或审批/结果与已有决定不一致 || Tool is not waiting for approval or a result, or the submission conflicts with an existing decision"
// @Router /v1/tasks/{id}/tools/{call}/result [post]
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

// listEvents godoc
// @ID executionListEvents
// @Summary 会话事件列表 || List session events
// @Description 按 sequence 升序返回会话在 after 之后的事件（每批最多 200 条）。after 也可以通过 Last-Event-ID 请求头提供。 || Return session events after the cursor, ordered by sequence ascending, up to 200 per batch. The cursor may also be supplied through the Last-Event-ID header.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Param after query int64 false "从该事件序号之后读取，默认 0 || Read after this event sequence; defaults to 0" minimum(0) default(0)
// @Param Last-Event-ID header string false "事件序号的备用游标；after 非空时优先使用 after || Fallback event-sequence cursor; a nonempty after takes precedence"
// @Success 200 {object} EventsResponse
// @Failure 400 {object} apierr.Envelope "after/Last-Event-ID 不是非负整数 || after/Last-Event-ID must be a nonnegative integer"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Router /v1/sessions/{id}/events [get]
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

// streamEvents godoc
// @ID executionStreamEvents
// @Summary 订阅会话事件流 || Subscribe to session events
// @Description 以 SSE 持续推送会话事件。通过 after 查询参数或 Last-Event-ID 请求头从指定序号续读；游标超过事件历史时拒绝连接。连接期间每 15 秒发送 ping 注释保活。非空 after 优先于 Last-Event-ID；两者均未提供时从 0 续读。data 为完整 Event JSON，id 对应 sequence，event 对应 type。连接建立前的错误返回 JSON；开始推流后发生错误会断开连接，客户端应保存最后收到的序号再重连。 || Stream session events over SSE. Resume from the after query parameter or Last-Event-ID header; a cursor beyond event history is rejected. A ping comment is sent every 15 seconds. A nonempty after takes precedence over Last-Event-ID; when neither is provided, resume from 0. data contains the full Event JSON, id is sequence, and event is type. Errors before streaming return JSON; errors during streaming close the connection. Save the last received sequence before reconnecting.
// @Description
// @Description 示例（每条消息由空行结束）： || Example (each message ends with a blank line):
// @Description ```text
// @Description id: 1
// @Description event: task.created
// @Description data: {"session_id":"session_example","sequence":1,"task_id":"task_example","type":"task.created","data":{"task_id":"task_example"},"created_at":"2026-09-13T00:00:00Z"}
// @Description
// @Description : ping
// @Description ```
// @Tags execution
// @Produce text/event-stream,json
// @Security BearerAuth
// @Param id path string true "会话 ID || Session ID"
// @Param after query int64 false "从该事件序号之后开始，默认 0 || Start after this event sequence; defaults to 0" minimum(0) default(0)
// @Param Last-Event-ID header string false "断线续读：最后收到的事件序号；after 非空时优先使用 after || Resume after the last received event sequence; a nonempty after takes precedence"
// @Success 200 {string} string "SSE 事件流 || SSE event stream"
// @Failure 400 {object} apierr.Envelope "游标不合法或超过事件历史 || Cursor is invalid or beyond event history"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "会话不存在或不在当前所有者范围内 || Session not found or outside the current owner's scope"
// @Router /v1/sessions/{id}/events/stream [get]
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
	defer ctl.SetWriteDeadline(time.Time{})
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
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
		select {
		case <-c.Request.Context().Done():
			return
		case <-tick.C:
		case <-ping.C:
			_ = ctl.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, e = fmt.Fprint(c.Writer, ": ping\n\n"); e != nil {
				return
			}
		}
	}
}

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
