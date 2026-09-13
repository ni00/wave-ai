package vault

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

type Credential struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID        string            `gorm:"type:uuid;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;index" json:"-"`
	Name         string            `json:"name" validate:"required"`
	Host         string            `json:"host" validate:"required"`
	Ciphertext   []byte            `json:"-"`
	Nonce        []byte            `json:"-"`
	Revoked      bool              `json:"revoked" validate:"required"`
	CreatedAt    time.Time         `json:"created_at" validate:"required" format:"date-time"`
}

// CreateRequest is the POST /v1/credentials request body.
type CreateRequest struct {
	Name  string `json:"name" binding:"required"`
	Host  string `json:"host" binding:"required"`
	Token string `json:"token" binding:"required"`
}

// ListResponse is the paginated list envelope for credentials.
type ListResponse struct {
	Data       []Credential `json:"data" validate:"required"`
	NextOffset int          `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
