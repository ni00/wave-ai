package skills

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

// Each upload has an immutable ID. Agent snapshots keep exact package IDs.
type Skill struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID        string            `gorm:"type:uuid;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;index" json:"-"`
	Description  string            `json:"description" validate:"required"`
	Name         string            `json:"name" validate:"required"`
	BlobKey      string            `json:"-"`
	CreatedAt    time.Time         `json:"created_at" validate:"required" format:"date-time"`
}

// ListResponse is the paginated list envelope for skills.
type ListResponse struct {
	Data       []Skill `json:"data" validate:"required"`
	NextOffset int     `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
