package memory

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/xid"
)

type handler struct{ db *gorm.DB }

func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := handler{db: db}
	r.DELETE("/memory-stores/:id/entries", h.deleteEntry)
	r.GET("/memory-stores/:id/revisions", h.listRevisions)

	r.POST("/memory-stores", h.createStore)
	r.GET("/memory-stores", h.listStores)
	r.GET("/memory-stores/:id/entries", h.listEntries)
	r.PUT("/memory-stores/:id/entries", h.writeEntry)
	r.GET("/memory-stores/:id/conflicts", h.listConflicts)
}

// deleteEntry godoc
// @ID memoryDeleteEntry
// @Summary 删除记忆条目 || Delete a memory entry
// @Description 按乐观锁删除指定路径的记忆条目；版本不匹配时保留冲突内容并返回 409，产生一条删除修订记录。 || Delete a memory entry at the specified path using optimistic locking. A version mismatch retains the conflict and returns 409; a successful deletion creates a deletion revision.
// @Tags memory
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "记忆库 ID || Memory store ID"
// @Param body body DeleteEntryRequest true "目标路径与期望版本 || Target path and expected version"
// @Success 200 {object} Entry
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或路径不是相对路径 || Invalid JSON body or non-relative path"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "记忆库不存在或不在当前所有者范围内 || Memory store not found or outside the current owner's scope"
// @Failure 409 {object} apierr.Envelope "版本冲突，冲突内容已被保留 || Version conflict; conflicting content retained"
// @Router /v1/memory-stores/{id}/entries [delete]
func (h handler) deleteEntry(c *gin.Context) {
	var in DeleteEntryRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	row, e := Delete(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Path, "", in.Version)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// listRevisions godoc
// @ID memoryListRevisions
// @Summary 记忆修订历史 || Memory revision history
// @Description 按修订 ID 倒序分页返回该记忆库的所有修订（含写入与删除）。 || Return all revisions of the memory store, including writes and deletions, paginated by revision ID descending.
// @Tags memory
// @Produce json
// @Security BearerAuth
// @Param id path string true "记忆库 ID || Memory store ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} RevisionsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "记忆库不存在或不在当前所有者范围内 || Memory store not found or outside the current owner's scope"
// @Router /v1/memory-stores/{id}/revisions [get]
func (h handler) listRevisions(c *gin.Context) {
	if _, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Revision{}
	if e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "store_id", Value: c.Param("id")}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// createStore godoc
// @ID memoryCreateStore
// @Summary 创建记忆库 || Create a memory store
// @Description 为当前所有者创建一个命名的记忆库。 || Create a named memory store for the current owner.
// @Tags memory
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateStoreRequest true "记忆库名称 || Memory store name"
// @Success 201 {object} Store
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或缺少必填字段 || Invalid JSON body or missing required fields"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/memory-stores [post]
func (h handler) createStore(c *gin.Context) {
	var in CreateStoreRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	p := c.MustGet("principal").(*auth.Principal)
	s := Store{ID: xid.New("memory"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: in.Name}
	if e := h.db.WithContext(c.Request.Context()).Create(&s).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(201, s)
}

// listStores godoc
// @ID memoryListStores
// @Summary 列出记忆库 || List memory stores
// @Description 分页返回当前所有者可见的记忆库。 || Return memory stores visible to the current owner, with pagination.
// @Tags memory
// @Produce json
// @Security BearerAuth
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ListResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Router /v1/memory-stores [get]
func (h handler) listStores(c *gin.Context) {
	rows := []Store{}
	if e := auth.Owned(h.db.WithContext(c.Request.Context()), c.MustGet("principal").(*auth.Principal)).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// listEntries godoc
// @ID memoryListEntries
// @Summary 列出记忆条目 || List memory entries
// @Description 按路径排序分页返回该记忆库的全部条目（含已删除标记的条目）。 || Return all entries in the memory store, including entries marked deleted, paginated and ordered by path.
// @Tags memory
// @Produce json
// @Security BearerAuth
// @Param id path string true "记忆库 ID || Memory store ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} EntriesResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "记忆库不存在或不在当前所有者范围内 || Memory store not found or outside the current owner's scope"
// @Router /v1/memory-stores/{id}/entries [get]
func (h handler) listEntries(c *gin.Context) {
	if _, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Entry{}
	if e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "store_id", Value: c.Param("id")}).Order(clause.OrderByColumn{Column: clause.Column{Name: "path"}}).Scopes(httpx.Page(c)).Find(&rows).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}

// writeEntry godoc
// @ID memoryWriteEntry
// @Summary 写入记忆条目 || Write a memory entry
// @Description 按乐观锁写入指定路径的记忆条目；版本匹配才生效并产生新版本与修订记录，版本不匹配时保留冲突内容并返回 409。 || Write a memory entry at the specified path using optimistic locking. A matching version creates a new version and revision; a mismatch retains the conflicting content and returns 409.
// @Tags memory
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "记忆库 ID || Memory store ID"
// @Param body body WriteEntryRequest true "目标路径、内容与期望版本 || Target path, content and expected version"
// @Success 200 {object} Entry
// @Failure 400 {object} apierr.Envelope "请求体不是合法 JSON 或路径不是相对路径 || Invalid JSON body or non-relative path"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "记忆库不存在或不在当前所有者范围内 || Memory store not found or outside the current owner's scope"
// @Failure 409 {object} apierr.Envelope "版本冲突，冲突内容已被保留 || Version conflict; conflicting content retained"
// @Router /v1/memory-stores/{id}/entries [put]
func (h handler) writeEntry(c *gin.Context) {
	var in WriteEntryRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Error(c, apierr.Invalid("invalid JSON: %v", err))
		return
	}
	row, e := Write(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id"), in.Path, in.Content, "", in.Version)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	c.JSON(200, row)
}

// listConflicts godoc
// @ID memoryListConflicts
// @Summary 列出记忆冲突 || List memory conflicts
// @Description 分页返回该记忆库因版本冲突被保留的冲突记录，包含期望版本、实际版本与冲突内容。 || Return retained version-conflict records for the memory store, with pagination, including expected and actual versions and conflicting content.
// @Tags memory
// @Produce json
// @Security BearerAuth
// @Param id path string true "记忆库 ID || Memory store ID"
// @Param limit query int false "每页条数 || Items per page" minimum(1) maximum(200) default(100)
// @Param offset query int false "偏移量 || Offset" minimum(0) default(0)
// @Success 200 {object} ConflictsResponse
// @Failure 400 {object} apierr.Envelope "分页参数不合法 || Invalid pagination parameters"
// @Failure 401 {object} apierr.Envelope "缺少或无效的 Bearer Key || Missing or invalid Bearer Key"
// @Failure 403 {object} apierr.Envelope "Key 不具备 API scope || Key does not have API scope"
// @Failure 500 {object} apierr.Envelope "内部错误，可用 request_id 排查 || Internal error; use request_id for troubleshooting"
// @Failure 404 {object} apierr.Envelope "记忆库不存在或不在当前所有者范围内 || Memory store not found or outside the current owner's scope"
// @Router /v1/memory-stores/{id}/conflicts [get]
func (h handler) listConflicts(c *gin.Context) {
	if _, e := Get(c.Request.Context(), h.db, c.MustGet("principal").(*auth.Principal), c.Param("id")); e != nil {
		httpx.Error(c, e)
		return
	}
	rows := []Conflict{}
	e := h.db.WithContext(c.Request.Context()).Where(clause.Eq{Column: "store_id", Value: c.Param("id")}).Scopes(httpx.Page(c)).Find(&rows).Error
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.List(c, rows)
}
