package vault

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

// createVault godoc
// @ID vaultsCreate
// @Summary 创建凭据库 || Create a vault
// @Tags vaults
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body VaultRequest true "请求 || Request"
// @Success 200 {object} Vault
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/vaults [post]
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

// listVaults godoc
// @ID vaultsList
// @Summary 列出凭据库 || List vaults
// @Tags vaults
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} VaultsResponse
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/vaults [get]
func (h handler) listVaults(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	rows := []Vault{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), p).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// getVault godoc
// @ID vaultsGet
// @Summary 查看凭据库 || Get a vault
// @Tags vaults
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Success 200 {object} Vault
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/vaults/{id} [get]
func (h handler) getVault(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	v, e := Get(c.Request.Context(), h.db, p, c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, v)
}

// archiveVault godoc
// @ID vaultsArchive
// @Summary 归档凭据库 || Archive a vault
// @Tags vaults
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Success 200 {object} Vault
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/vaults/{id} [delete]
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

// rotate godoc
// @ID vaultRotate
// @Summary 轮换凭据 || Rotate a credential
// @Tags credentials
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param body body RotateRequest true "请求 || Request"
// @Success 200 {object} Credential
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/credentials/{id} [put]
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

// validate godoc
// @ID vaultValidate
// @Summary 校验凭据绑定与加密数据 || Validate credential binding and encryption
// @Tags credentials
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "资源 ID || Resource ID"
// @Param body body ValidateRequest true "请求 || Request"
// @Success 200 {object} Validation
// @Failure 400 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 401 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 403 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 404 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 409 {object} apierr.Envelope "请求失败 || Request failed"
// @Failure 500 {object} apierr.Envelope "请求失败 || Request failed"
// @Router /v1/credentials/{id}/validate [post]
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
