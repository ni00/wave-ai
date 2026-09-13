package environments

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

type Environment struct {
	Organization auth.Organization   `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity       `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string              `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID        string              `gorm:"type:uuid;index" json:"-"`
	OwnerID      string              `gorm:"type:uuid;index" json:"-"`
	Name         string              `json:"name" binding:"required"`
	Packages     map[string][]string `gorm:"serializer:json;type:jsonb" json:"packages" validate:"required" extensions:"x-nullable"`
	Archived     bool                `json:"archived" validate:"required"`
	CreatedAt    time.Time           `json:"created_at" validate:"required" format:"date-time"`
}

// CreateRequest is the POST /v1/environments request body.
type CreateRequest struct {
	Name     string              `json:"name" binding:"required"`
	Packages map[string][]string `json:"packages" extensions:"x-nullable"`
}

// ListResponse is the paginated list envelope for environments.
type ListResponse struct {
	Data       []Environment `json:"data" validate:"required"`
	NextOffset int           `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
