package environments

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
	r.POST("/environments", h.create)
	r.GET("/environments", h.list)
	r.GET("/environments/:id", h.get)
	r.POST("/environments/:id/archive", h.archive)
}

// create godoc
// @ID environmentsCreate
// @Summary 创建环境 || Create an environment
// @Description 为当前所有者创建一个环境配置，声明各包管理器（apt、npm、pip、go、cargo、gem）要安装的包。 || Create an environment configuration for the current owner, declaring packages to install with apt, npm, pip, go, cargo and gem.
// @Tags environments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateRequest true "环境名称与包清单 || Environment name and package list"
// @Success 201 {object} Environment
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON、包管理器不受支持或包名为空 || Invalid JSON body, unsupported package manager or empty package name"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/environments [post]
func (h handler) create(c *gin.Context) {
	var in CreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	switch in.SandboxProfile {
	case "", "default", "standard", "large":
	default:
		httpx.Error(c, apierr.Invalid("sandbox_profile must be default, standard or large"))
		return
	}
	switch in.SandboxBackend {
	case "", "sbx", "gvisor", "podman":
	default:
		httpx.Error(c, apierr.Invalid("sandbox_backend must be sbx, gvisor or podman"))
		return
	}
	for manager, pkgs := range in.Packages {
		switch manager {
		case "apt", "npm", "pip", "go", "cargo", "gem":
		default:
			httpx.Error(c, apierr.Invalid("unsupported package manager"))
			return
		}
		for _, pkg := range pkgs {
			if pkg == "" {
				httpx.Error(c, apierr.Invalid("empty package name"))
				return
			}
		}
	}
	p := c.MustGet("principal").(*auth.Principal)
	row := Environment{ID: xid.New("env"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: in.Name, Packages: in.Packages, SandboxProfile: in.SandboxProfile, SandboxBackend: in.SandboxBackend}
	if e := h.db.WithContext(c.Request.Context()).Create(&row).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, row)
}

// list godoc
// @ID environmentsList
// @Summary 列出环境 || List environments
// @Description 分页返回当前所有者可见的环境配置。 || Return environment configurations visible to the current owner, with pagination.
// @Tags environments
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/environments [get]
func (h handler) list(c *gin.Context) {
	rows := []Environment{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// get godoc
// @ID environmentsGet
// @Summary 获取环境 || Get an environment
// @Description 按 ID 返回单个环境配置。 || Return an environment configuration by ID.
// @Tags environments
// @Produce json
// @Security BearerAuth
// @Param id path string true "环境 ID || Environment ID"
// @Success 200 {object} Environment
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "环境不存在或不在当前所有者范围内 || Environment not found or outside the current owner's scope"
// @Router /v1/environments/{id} [get]
func (h handler) get(c *gin.Context) {
	row, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// archive godoc
// @ID environmentsArchive
// @Summary 归档环境 || Archive an environment
// @Description 将环境标记为 archived，归档后不能再被部署引用。 || Mark the environment as archived; deployments can no longer reference it.
// @Tags environments
// @Produce json
// @Security BearerAuth
// @Param id path string true "环境 ID || Environment ID"
// @Success 204 "已归档 || Archived"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "环境不存在或不在当前所有者范围内 || Environment not found or outside the current owner's scope"
// @Router /v1/environments/{id}/archive [post]
func (h handler) archive(c *gin.Context) {
	row, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e == nil {
		e = h.db.WithContext(c.Request.Context()).Model(&row).Update("archived", true).Error
	}
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}
