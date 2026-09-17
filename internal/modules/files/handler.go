package files

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/httpx"
)

type handler struct {
	db *gorm.DB
	b  *blobstore.Store
}

func Register(r *gin.RouterGroup, db *gorm.DB, b *blobstore.Store) {
	h := handler{db: db, b: b}
	r.POST("/files", h.upload)
	r.GET("/files", h.list)
	r.GET("/files/:id", h.get)
	r.GET("/files/:id/content", h.content)
	r.DELETE("/files/:id", h.delete)
}

// upload handles the documented HTTP operation.
func (h handler) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	header, e := c.FormFile("file")
	if e != nil {
		httpx.Error(c, apierr.Invalid("file upload required (maximum 32 MiB)"))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	f, e := header.Open()
	if e != nil {
		httpx.Error(c, e)
		return
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 32<<20))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	row, e := Put(c.Request.Context(), h.db, h.b, c.MustGet("principal").(*auth.Principal), "", header.Filename, data)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, row)
}

// list handles the documented HTTP operation.
func (h handler) list(c *gin.Context) {
	rows := []File{}
	q := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Where(clause.Eq{Column: "deleted", Value: false})
	if sid := c.Query("session_id"); sid != "" {
		q = q.Where(clause.Eq{Column: "session_id", Value: sid})
	}
	if taskID := c.Query("task_id"); taskID != "" {
		q = q.Where(clause.Eq{Column: "task_id", Value: taskID})
	}
	e := q.Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
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

// content handles the documented HTTP operation.
func (h handler) content(c *gin.Context) {
	row, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	f, e := h.b.Open(c.Request.Context(), blobstore.Scope{OrgID: row.OrgID, OwnerID: row.OwnerID}, row.BlobKey)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	defer f.Close()
	httpx.Download(c.Writer, c.Request, row.Name, row.CreatedAt, f)
}

// delete handles the documented HTTP operation.
func (h handler) delete(c *gin.Context) {
	row, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e == nil {
		e = h.db.WithContext(c.Request.Context()).Model(&row).Update("deleted", true).Error
	}
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.Status(204)
}
