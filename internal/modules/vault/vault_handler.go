package vault

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

// createVault handles the documented HTTP operation.
func (h handler) createVault(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var in VaultRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON"))
		return
	}
	v := Vault{ID: xid.New("vault"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: in.Name}
	if e := h.db.WithContext(c.Request.Context()).Create(&v).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, v)
}

// listVaults handles the documented HTTP operation.
func (h handler) listVaults(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	rows := []Vault{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), p).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// getVault handles the documented HTTP operation.
func (h handler) getVault(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	v, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, v)
}

// archiveVault handles the documented HTTP operation.
func (h handler) archiveVault(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	v, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	v.Archived = true
	if e = h.db.WithContext(c.Request.Context()).Save(&v).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, v)
}

// rotate handles the documented HTTP operation.
func (h handler) rotate(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var in RotateRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON"))
		return
	}
	v, e := Rotate(c.Request.Context(), h.db, h.box, p, c.Param("id"), in)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, v)
}

// validate handles the documented HTTP operation.
func (h handler) validate(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	var in ValidateRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON"))
		return
	}
	_, e := Bearer(c.Request.Context(), h.db, h.box, p, c.Param("id"), in.Target)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	var credential Credential
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), p).Where(clause.Eq{Column: "id", Value: c.Param("id")}).Take(&credential).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, Validation{Valid: true, Version: credential.Version})
}
