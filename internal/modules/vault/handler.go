package vault

import (
	"net/url"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/secrets"
	"wave-ai.local/wave/internal/platform/xid"
)

type handler struct {
	db  *gorm.DB
	box *secrets.Box
}

func Register(r *gin.RouterGroup, db *gorm.DB, box *secrets.Box) {
	h := handler{db: db, box: box}
	r.POST("/credentials", h.create)
	r.GET("/credentials", h.list)
	r.DELETE("/credentials/:id", h.revoke)
	r.PUT("/credentials/:id", h.rotate)
	r.POST("/credentials/:id/validate", h.validate)
	r.POST("/vaults", h.createVault)
	r.GET("/vaults", h.listVaults)
	r.GET("/vaults/:id", h.getVault)
	r.DELETE("/vaults/:id", h.archiveVault)
}

// create handles the documented HTTP operation.
func (h handler) create(c *gin.Context) {
	var in CreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	u, e := url.Parse("https://" + in.Host)
	if e != nil || u.Host != in.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" {
		httpx.Error(c, apierr.Invalid("host must be an exact authority without path or user info"))
		return
	}
	if len(in.Token) > 65536 {
		httpx.Error(c, apierr.Invalid("token exceeds 65536 bytes"))
		return
	}
	cipher, nonce, e := h.box.Seal([]byte(in.Token))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	if in.VaultID != "" {
		v, e := Get(c.Request.Context(), h.db, p, in.VaultID)
		if e != nil {
			httpx.Error(c, e)
			return
		}
		if v.Archived {
			httpx.Error(c, apierr.Invalid("vault archived"))
			return
		}
	}
	row := Credential{VaultID: in.VaultID, Version: 1, ID: xid.New("credential"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: in.Name, Host: in.Host, Ciphertext: cipher, Nonce: nonce}
	if e = h.db.WithContext(c.Request.Context()).Create(&row).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, row)
}

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	rows := []Credential{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c), func(db *gorm.DB) *gorm.DB {
		if id := c.Query("vault_id"); id != "" {
			return db.Where(clause.Eq{Column: "vault_id", Value: id})
		}
		return db
	}).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// revoke handles the documented HTTP operation.
func (h handler) revoke(c *gin.Context) {
	var row Credential
	e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Where(clause.Eq{Column: "id", Value: c.Param("id")}).Take(&row).Error
	if e == nil {
		e = h.db.WithContext(c.Request.Context()).Model(&row).Update("revoked", true).Error
	}
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}
