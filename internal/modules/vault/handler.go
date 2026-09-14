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

// create godoc
// @ID vaultCreate
// @Summary 录入凭据 || Store a credential
// @Description 为当前所有者录入一条主机凭据。凭据 token 仅在写入时加密存储，接口不提供任何读取明文的途径，响应中只返回元数据。 || Store a host credential for the current owner. The token is encrypted on write; the API never exposes plaintext and returns metadata only.
// @Tags vault
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateRequest true "凭据名称、主机与 token || Credential name, host and token"
// @Success 201 {object} Credential
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或 host 不是不含路径与用户信息的精确 authority || Invalid JSON body, or host is not an exact authority without a path or user information"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/credentials [post]
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

// list godoc
// @ID vaultList
// @Summary 列出凭据 || List credentials
// @Description 分页返回当前所有者可见的凭据元数据。凭据 token 只写不读，响应中永远不包含明文或密文。 || Return credential metadata visible to the current owner, with pagination. Tokens are write-only; responses never contain plaintext or ciphertext.
// @Tags vault
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param vault_id query string false "凭据库 ID || Vault ID"
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/credentials [get]
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

// revoke godoc
// @ID vaultRevoke
// @Summary 吊销凭据 || Revoke a credential
// @Description 将凭据标记为 revoked，此后不再可用于任务执行；凭据记录与密文保留用于审计，不提供明文读取。 || Mark the credential as revoked so it can no longer be used for task execution. Its record and ciphertext are retained for auditing; plaintext cannot be read.
// @Tags vault
// @Produce json
// @Security BearerAuth
// @Param id path string true "凭据 ID || Credential ID"
// @Success 204 "已吊销 || Revoked"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "凭据不存在或不在当前所有者范围内 || Credential not found or outside the current owner's scope"
// @Router /v1/credentials/{id} [delete]
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
