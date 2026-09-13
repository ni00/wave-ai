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

// upload godoc
// @ID filesUpload
// @Summary 上传文件 || Upload a file
// @Description 通过 multipart/form-data 上传单个文件，整个 multipart 请求体上限 32 MiB（含边界与字段开销）。内容按所有者维度以 SHA-256 对象键写入对象存储，相同内容在同一所有者内复用，不会重复存储。返回新建的文件元数据。 || Upload one file using multipart/form-data. The entire multipart request is limited to 32 MiB, including boundaries and field overhead. Content is stored under owner-scoped SHA-256 object keys; identical content is reused within the same owner. Return metadata for the newly created file.
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "要上传的文件 || File to upload"
// @Success 201 {object} File
// @Failure 400 {object} apierr.Envelope "缺少 file 字段、文件名非法或超过 32 MiB 上限 || Missing file field, invalid filename or exceeded 32 MiB limit"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/files [post]
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

// list godoc
// @ID filesList
// @Summary 列出文件 || List files
// @Description 分页返回未删除的文件元数据，按 id 排序；可用 session_id 或 task_id 过滤。 || Return metadata for files that are not deleted, paginated and ordered by id; filter by session_id or task_id.
// @Tags files
// @Produce json
// @Security BearerAuth
// @Param session_id query string false "按会话 ID 过滤 || Filter by session ID"
// @Param task_id query string false "按任务 ID 过滤 || Filter by task ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/files [get]
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

// get godoc
// @ID filesGet
// @Summary 获取文件元数据 || Get file metadata
// @Description 按 ID 返回单个未删除文件的元数据，不含内容。 || Return metadata for a non-deleted file by ID, without its content.
// @Tags files
// @Produce json
// @Security BearerAuth
// @Param id path string true "文件 ID || File ID"
// @Success 200 {object} File
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "文件不存在、已删除或不在当前所有者范围内 || File not found, deleted or outside the current owner's scope"
// @Router /v1/files/{id} [get]
func (h handler) get(c *gin.Context) {
	row, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"))
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// content godoc
// @ID filesContent
// @Summary 下载文件内容 || Download file content
// @Description 按 ID 读取文件内容，经身份和归属校验后由服务端流式返回；文件按附件（Content-Disposition: attachment）发送，支持 Range 请求，禁止浏览器缓存。 || Download file content by ID after authentication and ownership checks. The server streams it with Content-Disposition: attachment and Range support; browser caching is disabled.
// @Tags files
// @Produce application/octet-stream,multipart/byteranges,json,plain
// @Security BearerAuth
// @Param id path string true "文件 ID || File ID"
// @Param Range header string false "字节范围，如 bytes=0-1023 || Byte range, for example bytes=0-1023"
// @Param If-Range header string false "仅当 Last-Modified 日期匹配时返回范围，否则返回完整文件 || Return the range only if the Last-Modified date matches; otherwise return the full file"
// @Param If-Modified-Since header string false "未修改时返回 304（HTTP 日期） || Return 304 if unmodified (HTTP date)"
// @Param If-Unmodified-Since header string false "此日期后有修改时返回 412（HTTP 日期） || Return 412 if modified after this date (HTTP date)"
// @Success 200 {file} file "文件二进制内容 || File bytes"
// @Success 206 {file} file "部分内容；多个范围返回 multipart/byteranges || Partial content; multiple ranges return multipart/byteranges"
// @Success 304 "未修改，无响应体 || Not modified; empty body"
// @Failure 412 "条件请求失败，无响应体 || Precondition failed; empty body"
// @Failure 416 {string} string "Range 无效或越界；底层下载错误为 text/plain || Invalid or unsatisfiable Range; underlying download errors use text/plain"
// @Header 200,206 {string} Content-Disposition "attachment; filename=..."
// @Header 200,206 {string} Accept-Ranges "bytes"
// @Header 200,206 {string} Last-Modified "HTTP 日期 || HTTP date"
// @Header 206 {string} Content-Range "单范围响应：bytes start-end/total || Single-range response: bytes start-end/total"
// @Header 416 {string} Content-Range "范围越界时：bytes */total || For an unsatisfiable range: bytes */total"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "文件不存在、已删除或不在当前所有者范围内 || File not found, deleted or outside the current owner's scope"
// @Router /v1/files/{id}/content [get]
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

// delete godoc
// @ID filesDelete
// @Summary 删除文件 || Delete a file
// @Description 软删除：仅将文件标记为 deleted，普通接口和新引用不再可见；已有会话引用仍可使用，对象存储内容不立即擦除。 || Soft-delete the file by marking it deleted. It is hidden from ordinary APIs and new references, while existing session references remain usable; object-store content is not immediately erased.
// @Tags files
// @Produce json
// @Security BearerAuth
// @Param id path string true "文件 ID || File ID"
// @Success 204 "已删除 || Deleted"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "文件不存在、已删除或不在当前所有者范围内 || File not found, deleted or outside the current owner's scope"
// @Router /v1/files/{id} [delete]
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
