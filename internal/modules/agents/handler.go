package agents

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
	r.POST("/agents", h.create)
	r.GET("/agents", h.list)
	r.GET("/agents/:id", h.get)
	r.PUT("/agents/:id", h.update)
	r.POST("/agents/:id/archive", h.archive)
	r.GET("/agents/:id/versions", h.versions)
}

// create godoc
// @ID agentsCreate
// @Summary 创建 Agent || Create an Agent
// @Description 创建一个带初始配置（版本 1）的 Agent。配置不可变字段之外的内容通过 PUT /v1/agents/:id 更新并产生新版本。 || Create an Agent with an initial configuration (version 1). Update mutable configuration fields through PUT /v1/agents/:id to create a new version.
// @Tags agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body Config true "Agent 配置 || Agent configuration"
// @Success 201 {object} Agent
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或缺少必填字段 || Invalid JSON body or missing required fields"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/agents [post]
func (h handler) create(c *gin.Context) {
	var in Config
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	a, e := Create(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, a)
}

// list godoc
// @ID agentsList
// @Summary 列出 Agent || List Agents
// @Description 分页返回当前所有者可见的 Agent，按 id 排序。 || Return Agents visible to the current owner, paginated and ordered by id.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/agents [get]
func (h handler) list(c *gin.Context) {
	rows := []Agent{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// get godoc
// @ID agentsGet
// @Summary 获取 Agent || Get an Agent
// @Description 按 ID 返回单个 Agent。 || Return an Agent by ID.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Param id path string true "Agent ID"
// @Success 200 {object} Agent
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "Agent 不存在或不在当前所有者范围内 || Agent not found or outside the current owner's scope"
// @Router /v1/agents/{id} [get]
func (h handler) get(c *gin.Context) {
	a, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, a)
}

// update godoc
// @ID agentsUpdate
// @Summary 更新 Agent || Update an Agent
// @Description 提交期望的 version 和新配置；版本匹配才生效，产生 version+1 的新配置版本。 || Submit the expected version and new configuration. A matching version creates a new configuration at version+1.
// @Tags agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Agent ID"
// @Param body body UpdateRequest true "期望版本与新配置 || Expected version and new configuration"
// @Success 200 {object} Agent
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或缺少必填字段 || Invalid JSON body or missing required fields"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "Agent 不存在 || Agent not found"
// @Failure 409 {object} apierr.Envelope "版本冲突 || Version conflict"
// @Router /v1/agents/{id} [put]
func (h handler) update(c *gin.Context) {
	var in UpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	a, e := Update(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Version, in.Config)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, a)
}

// archive godoc
// @ID agentsArchive
// @Summary 归档 Agent || Archive an Agent
// @Description 将 Agent 标记为 archived，不再出现在默认列表中。 || Mark the Agent as archived so it no longer appears in default lists.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Param id path string true "Agent ID"
// @Success 204 "已归档 || Archived"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "Agent 不存在 || Agent not found"
// @Router /v1/agents/{id}/archive [post]
func (h handler) archive(c *gin.Context) {
	a, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e == nil {
		e = h.db.WithContext(c.Request.Context()).Model(&a).Update("archived", true).Error
	}
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}

// versions godoc
// @ID agentsVersions
// @Summary Agent 版本历史 || Agent version history
// @Description 按版本号倒序返回该 Agent 的历史配置版本。 || Return the Agent's historical configurations by version descending.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Param id path string true "Agent ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} VersionsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "Agent 不存在 || Agent not found"
// @Router /v1/agents/{id}/versions [get]
func (h handler) versions(c *gin.Context) {
	a, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Version{}
	e = h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "agent_id", Value: a.ID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "number"}, Desc: true}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
