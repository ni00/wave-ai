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

// create handles the documented HTTP operation.
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
	case "", "gvisor":
	default:
		httpx.Error(c, apierr.Invalid("sandbox_backend must be gvisor"))
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

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	rows := []Environment{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// get handles the documented HTTP operation.
func (h handler) get(c *gin.Context) {
	row, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// archive handles the documented HTTP operation.
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
