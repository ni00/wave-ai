package files

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

type File struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ArtifactKey  *string           `gorm:"uniqueIndex" json:"-"`
	Path         string            `json:"path,omitempty"`
	ID           string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID        string            `gorm:"type:uuid;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;index" json:"-"`
	TaskID       string            `gorm:"index" json:"task_id,omitempty"`
	SessionID    string            `gorm:"index" json:"session_id,omitempty"`
	Name         string            `json:"name" validate:"required"`
	MIME         string            `json:"mime_type" validate:"required"`
	Size         int64             `json:"size" validate:"required" format:"int64"`
	BlobKey      string            `json:"-"`
	Deleted      bool              `json:"deleted" validate:"required"`
	CreatedAt    time.Time         `json:"created_at" validate:"required" format:"date-time"`
}

// ListResponse is the paginated list envelope for files.
type ListResponse struct {
	Data       []File `json:"data" validate:"required"`
	NextOffset int    `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
