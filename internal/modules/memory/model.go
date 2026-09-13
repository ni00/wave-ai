package memory

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

type Store struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID        string            `gorm:"type:uuid;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;index" json:"-"`
	Name         string            `json:"name" validate:"required"`
	CreatedAt    time.Time         `json:"created_at" validate:"required" format:"date-time"`
}
type Entry struct {
	StoreID   string    `gorm:"primaryKey" json:"store_id" validate:"required"`
	Path      string    `gorm:"primaryKey" json:"path" validate:"required"`
	Version   int       `json:"version" validate:"required"`
	Content   string    `json:"content" validate:"required"`
	SHA       string    `json:"sha256" validate:"required"`
	Deleted   bool      `json:"deleted" validate:"required"`
	UpdatedAt time.Time `json:"updated_at" validate:"required" format:"date-time"`
}
type Revision struct {
	Deleted   bool      `json:"deleted" validate:"required"`
	ID        string    `gorm:"primaryKey" json:"id" validate:"required"`
	StoreID   string    `gorm:"index" json:"store_id" validate:"required"`
	Path      string    `json:"path" validate:"required"`
	Version   int       `json:"version" validate:"required"`
	Content   string    `json:"content" validate:"required"`
	SessionID string    `json:"session_id,omitempty"`
	CreatedAt time.Time `json:"created_at" validate:"required" format:"date-time"`
}
type Conflict struct {
	ID        string    `gorm:"primaryKey" json:"id" validate:"required"`
	StoreID   string    `gorm:"index" json:"store_id" validate:"required"`
	Path      string    `json:"path" validate:"required"`
	Expected  int       `json:"expected_version" validate:"required"`
	Actual    int       `json:"actual_version" validate:"required"`
	Content   string    `json:"content" validate:"required"`
	CreatedAt time.Time `json:"created_at" validate:"required" format:"date-time"`
}
type Baseline struct {
	SessionID string `gorm:"primaryKey"`
	StoreID   string `gorm:"primaryKey"`
	Path      string `gorm:"primaryKey"`
	Version   int
	SHA       string
}

// CreateStoreRequest is the POST /v1/memory-stores request body.
type CreateStoreRequest struct {
	Name string `json:"name" binding:"required"`
}

// WriteEntryRequest is the PUT /v1/memory-stores/:id/entries request body.
type WriteEntryRequest struct {
	Path    string `json:"path" validate:"required" minLength:"1" example:"notes/project.md"`
	Content string `json:"content"`
	Version int    `json:"version"` // 期望版本；新条目使用 0，更新使用当前版本，不匹配返回 409。 || Expected version; use 0 for new entries and the current version for updates. A mismatch returns 409.
}

// DeleteEntryRequest is the DELETE /v1/memory-stores/:id/entries request body.
type DeleteEntryRequest struct {
	Path    string `json:"path" validate:"required" minLength:"1" example:"notes/project.md"`
	Version int    `json:"version"` // 期望版本；不匹配返回 409。 || Expected version; a mismatch returns 409.
}

// ListResponse is the paginated list envelope for memory stores.
type ListResponse struct {
	Data       []Store `json:"data" validate:"required"`
	NextOffset int     `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// EntriesResponse is the paginated list envelope for memory entries.
type EntriesResponse struct {
	Data       []Entry `json:"data" validate:"required"`
	NextOffset int     `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// RevisionsResponse is the paginated list envelope for memory revisions.
type RevisionsResponse struct {
	Data       []Revision `json:"data" validate:"required"`
	NextOffset int        `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// ConflictsResponse is the paginated list envelope for memory conflicts.
type ConflictsResponse struct {
	Data       []Conflict `json:"data" validate:"required"`
	NextOffset int        `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

func Models() []any { return []any{&Store{}, &Entry{}, &Revision{}, &Conflict{}, &Baseline{}} }

func (Store) TableName() string { return "memory_stores" }

func (Entry) TableName() string { return "memory_entries" }

func (Revision) TableName() string { return "memory_revisions" }

func (Conflict) TableName() string { return "memory_conflicts" }

func (Baseline) TableName() string { return "memory_baselines" }
