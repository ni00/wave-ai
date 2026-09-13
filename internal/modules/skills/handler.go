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

// upload godoc
// @ID skillsUpload
// @Summary 上传技能包 || Upload a skill package
// @Description 通过 multipart/form-data 上传技能 ZIP 包（请求体上限 24 MiB）。ZIP 根目录必须包含带 YAML frontmatter（name 和 description）的 SKILL.md，最多 200 个文件且路径必须安全；包内容解压后上限 20 MiB。技能 ID 不可变，创建后不可更换。 || Upload a skill ZIP using multipart/form-data, with a 24 MiB request limit. The ZIP root must contain SKILL.md with YAML frontmatter (name and description). At most 200 files with safe paths are allowed; extracted content is limited to 20 MiB. Skill IDs are immutable and packages cannot be replaced after creation.
// @Tags skills
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "包含根目录 SKILL.md 的 ZIP 技能包 || ZIP skill package containing SKILL.md at its root"
// @Success 201 {object} Skill
// @Failure 400 {object} apierr.Envelope "缺少 file 字段、ZIP 非法或超出大小/文件数限制、SKILL.md 缺失或元数据不合法 || Missing file field, invalid ZIP, exceeded size or file-count limits, missing SKILL.md or invalid metadata"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/skills [post]
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

// list godoc
// @ID skillsList
// @Summary 列出技能包 || List skill packages
// @Description 分页返回当前所有者可见的技能包元数据，按主键排序。 || Return skill-package metadata visible to the current owner, paginated and ordered by primary key.
// @Tags skills
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/skills [get]
func (h handler) list(c *gin.Context) {
	rows := []Skill{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// content godoc
// @ID skillsContent
// @Summary 下载技能包 || Download a skill package
// @Description 按 ID 读取技能 ZIP 原包，经身份和归属校验后由服务端流式返回，文件名为 `<name>.zip`，按附件发送，支持 Range 请求。 || Download the original skill ZIP by ID after authentication and ownership checks. The server streams it as an attachment named `<name>.zip`, with Range support.
// @Tags skills
// @Produce application/octet-stream,multipart/byteranges,json,plain
// @Security BearerAuth
// @Param id path string true "技能 ID || Skill ID"
// @Param Range header string false "字节范围，如 bytes=0-1023 || Byte range, for example bytes=0-1023"
// @Param If-Range header string false "仅当 Last-Modified 日期匹配时返回范围，否则返回完整文件 || Return the range only if the Last-Modified date matches; otherwise return the full file"
// @Param If-Modified-Since header string false "未修改时返回 304（HTTP 日期） || Return 304 if unmodified (HTTP date)"
// @Param If-Unmodified-Since header string false "此日期后有修改时返回 412（HTTP 日期） || Return 412 if modified after this date (HTTP date)"
// @Success 200 {file} file "技能 ZIP 包二进制内容 || Skill ZIP package bytes"
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
// @Failure 404 {object} apierr.Envelope "技能不存在或不在当前所有者范围内 || Skill not found or outside the current owner's scope"
// @Router /v1/skills/{id}/content [get]
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
