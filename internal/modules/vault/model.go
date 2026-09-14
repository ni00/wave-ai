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
	VaultID      string            `gorm:"index" json:"vault_id,omitempty"`
	Version      int               `gorm:"default:1" json:"version"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Name         string            `json:"name" validate:"required"`
	Host         string            `json:"host" validate:"required"`
	Ciphertext   []byte            `json:"-"`
	Nonce        []byte            `json:"-"`
	Revoked      bool              `json:"revoked" validate:"required"`
	CreatedAt    time.Time         `json:"created_at" validate:"required" format:"date-time"`
}

// CreateRequest is the POST /v1/credentials request body.
type CreateRequest struct {
	VaultID string `json:"vault_id"`
	Name    string `json:"name" binding:"required"`
	Host    string `json:"host" binding:"required"`
	Token   string `json:"token" binding:"required"`
}

// ListResponse is the paginated list envelope for credentials.
type ListResponse struct {
	Data       []Credential `json:"data" validate:"required"`
	NextOffset int          `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// Vault groups credentials that can be explicitly bound to a session.
type Vault struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string            `gorm:"primaryKey" json:"id"`
	OrgID        string            `gorm:"type:uuid;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;index" json:"-"`
	Name         string            `json:"name"`
	Archived     bool              `json:"archived"`
	CreatedAt    time.Time         `json:"created_at"`
}
type VaultRequest struct {
	Name string `json:"name" binding:"required"`
}
type VaultsResponse struct {
	Data       []Vault `json:"data"`
	NextOffset int     `json:"next_offset,omitempty"`
}
type RotateRequest struct {
	Token   string `json:"token" binding:"required"`
	Version int    `json:"version" binding:"required"`
}
type ValidateRequest struct {
	Target string `json:"target" binding:"required"`
}
type Validation struct {
	Valid   bool `json:"valid"`
	Version int  `json:"version"`
}
