package deployments

import (
	"wave-ai.local/wave/internal/platform/apierr"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
	r.POST("/deployments", h.create)
	r.GET("/deployments", h.list)
	r.GET("/deployments/:id", h.get)
	r.PUT("/deployments/:id", h.update)
	r.GET("/deployments/:id/versions", h.versions)
	r.GET("/deployments/:id/schedule", h.schedule)
	r.POST("/deployments/:id/run", h.run)
	r.PATCH("/deployments/:id", h.pause)
	r.GET("/deployments/:id/runs", h.runs)
}

// create godoc
// @ID deploymentsCreate
// @Summary 创建部署 || Create a deployment
// @Description 创建一个将 Agent 与可选环境绑定、按输入运行的部署；cron 为标准五段表达式，填写时计算下次触发时间，为空则仅手动触发。 || Create a deployment that binds an Agent to an optional environment and runs the supplied input. cron uses the standard five-field syntax; when set, the next run is calculated, otherwise runs are manual only.
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateRequest true "部署名称、Agent ID、可选环境 ID、输入与 cron || Deployment name, Agent ID, optional environment ID, input and cron"
// @Success 201 {object} Deployment
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或 cron 表达式不合法 || Invalid JSON body or cron expression"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "Agent 或环境不存在 || Agent or environment not found"
// @Router /v1/deployments [post]
func (h handler) create(c *gin.Context) {
	var in CreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	d, e := Create(c.Request.Context(), h.db, p, in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, d)
}

// list godoc
// @ID deploymentsList
// @Summary 列出部署 || List deployments
// @Description 分页返回当前所有者可见的部署。 || Return deployments visible to the current owner, with pagination.
// @Tags deployments
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/deployments [get]
func (h handler) list(c *gin.Context) {
	rows := []Deployment{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// run godoc
// @ID deploymentsRun
// @Summary 手动触发部署 || Trigger a deployment manually
// @Description 立即触发一次部署运行，创建会话与任务并异步执行；暂停时仍可手动触发。 || Trigger a deployment immediately, creating a session and task for asynchronous execution. Paused deployments can be triggered manually.
// @Tags deployments
// @Produce json
// @Security BearerAuth
// @Param id path string true "部署 ID || Deployment ID"
// @Success 202 {object} Run
// @Failure 400 {object} apierr.Envelope "环境或 Agent 已归档，或任务资源校验失败 || Environment or Agent is archived, or task resource validation failed"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "部署不存在或不在当前所有者范围内 || Deployment not found or outside the current owner's scope"
// @Param Idempotency-Key header string false "重试去重键 || Retry deduplication key"
// @Router /v1/deployments/{id}/run [post]
func (h handler) run(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	row, e := execution.Admit(c.Request.Context(), h.db, p, c.GetHeader("Idempotency-Key"), "deployment.run", c.Param("id"), func(tx *gorm.DB) (Run, error) { return Fire(c.Request.Context(), tx, p, c.Param("id"), false) })
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(202, row)
}

// pause godoc
// @ID deploymentsPause
// @Summary 暂停或恢复部署 || Pause or resume a deployment
// @Description 设置部署的暂停状态；恢复时会重新计算 cron 的下次触发时间。 || Set the deployment's paused state; resuming recalculates the next cron run.
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "部署 ID || Deployment ID"
// @Param body body PauseRequest true "目标暂停状态 || Desired paused state"
// @Success 200 {object} Deployment
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或缺少 paused 字段 || Invalid JSON body or missing paused field"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "部署不存在或不在当前所有者范围内 || Deployment not found or outside the current owner's scope"
// @Router /v1/deployments/{id} [patch]
func (h handler) pause(c *gin.Context) {
	var in PauseRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	d, e := Pause(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), *in.Paused)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, d)
}

// runs godoc
// @ID deploymentsRuns
// @Summary 部署运行记录 || Deployment run records
// @Description 分页返回该部署的触发记录，包含关联的任务与会话 ID。 || Return run records for the deployment, with pagination, including the associated task and session IDs.
// @Tags deployments
// @Produce json
// @Security BearerAuth
// @Param id path string true "部署 ID || Deployment ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} RunsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "部署不存在或不在当前所有者范围内 || Deployment not found or outside the current owner's scope"
// @Router /v1/deployments/{id}/runs [get]
func (h handler) runs(c *gin.Context) {
	var d Deployment
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Where(clause.Eq{Column: "id", Value: c.Param("id")}).Take(&d).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Run{}
	if e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "deployment_id", Value: d.ID}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
