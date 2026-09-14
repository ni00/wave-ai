package execution

import (
	"github.com/gin-gonic/gin"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/observe"
)

// trace godoc
// @ID executionTrace
// @Summary 任务执行链路与耗时 || Task trace and timing breakdown
// @Description 返回当前任务的模型、工具、准备和收尾 span，以及子任务链接。trace_id 等于 root_id。摘要只统计当前任务，不重复累加子任务；初始排队不包含后续等待，未归因耗时按区间并集计算。仅含元数据，不含提示词、命令、结果和错误文本。最多 5000 次模型调用、5000 次工具调用、10000 条阶段事件和 5000 个子任务；超出标记 truncated，缺失结束时间标记 incomplete，未知耗时为 null。 || Return model, tool, preparation and finalization spans with child-task links. trace_id equals root_id. Summaries cover only this task; initial queue excludes later waits, and unattributed time uses the union of intervals. Metadata only: no prompts, commands, outputs or error text. Bounded to 5000 generations, 5000 tools, 10000 phase events and 5000 children; truncated and incomplete explicitly flag limits or missing ends. Unknown durations are null.
// @Tags execution
// @Produce json
// @Security BearerAuth
// @Param id path string true "任务 ID || Task ID"
// @Success 200 {object} Trace
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 404 {object} apierr.Envelope "任务不存在或不在当前所有者范围内 || Task not found or outside the current owner's scope"
// @Failure 500 {object} apierr.Envelope "内部错误 || Internal error"
// @Router /v1/tasks/{id}/trace [get]
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
