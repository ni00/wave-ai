package skills

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

type handler struct {
	db *gorm.DB
	b  *blobstore.Store
}

func Register(r *gin.RouterGroup, db *gorm.DB, b *blobstore.Store) {
	h := handler{db: db, b: b}
	r.POST("/skills", h.upload)
	r.GET("/skills", h.list)
	r.GET("/skills/:id/content", h.content)
}

// upload handles the documented HTTP operation.
func (h handler) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 24<<20)
	h2, e := c.FormFile("file")
	if e != nil {
		httpx.Error(c, apierr.Invalid("ZIP file required"))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	f, e := h2.Open()
	if e != nil {
		httpx.Error(c, e)
		return
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 24<<20))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	items, e := Unpack(raw)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	meta, e := ParseMetadata(items["SKILL.md"])
	if e != nil {
		httpx.Error(c, e)
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	key, _, e := h.b.PutBytes(c.Request.Context(), blobstore.Scope{OrgID: p.OrgID, OwnerID: p.PrincipalID}, raw)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	s := Skill{ID: xid.New("skill"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: meta.Name, Description: meta.Description, BlobKey: key}
	if e = h.db.WithContext(c.Request.Context()).Create(&s).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, s)
}

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	rows := []Skill{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// content handles the documented HTTP operation.
func (h handler) content(c *gin.Context) {
	s, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	f, e := h.b.Open(c.Request.Context(), blobstore.Scope{OrgID: s.OrgID, OwnerID: s.OwnerID}, s.BlobKey)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	defer f.Close()
	httpx.Download(c.Writer, c.Request, s.Name+".zip", s.CreatedAt, f)
}
