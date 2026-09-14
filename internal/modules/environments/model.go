package environments

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

type Environment struct {
	Organization   auth.Organization   `gorm:"foreignKey:OrgID" json:"-"`
	Owner          auth.Identity       `gorm:"foreignKey:OwnerID" json:"-"`
	ID             string              `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID          string              `gorm:"type:uuid;index" json:"-"`
	OwnerID        string              `gorm:"type:uuid;index" json:"-"`
	Name           string              `json:"name" binding:"required"`
	Packages       map[string][]string `gorm:"serializer:json;type:jsonb" json:"packages" validate:"required" extensions:"x-nullable"`
	SandboxProfile string              `gorm:"not null;default:''" json:"sandbox_profile,omitempty" enums:"default,standard,large"` // 省略使用服务默认；standard 为 2 CPU/2048 MiB，large 为 2 CPU/4096 MiB。 || Omit for service defaults; standard uses 2 CPUs/2048 MiB, large uses 2 CPUs/4096 MiB.
	SandboxBackend string              `gorm:"not null;default:''" json:"sandbox_backend,omitempty" enums:"sbx,gvisor,podman"`      // 省略使用服务默认；已有会话保留首次选择的后端。 || Omit for service defaults; existing sessions retain their original backend.
	Archived       bool                `json:"archived" validate:"required"`
	CreatedAt      time.Time           `json:"created_at" validate:"required" format:"date-time"`
}

// CreateRequest is the POST /v1/environments request body.
type CreateRequest struct {
	Name           string              `json:"name" binding:"required"`
	Packages       map[string][]string `json:"packages" extensions:"x-nullable"`
	SandboxProfile string              `json:"sandbox_profile,omitempty" enums:"default,standard,large"` // 省略使用服务默认；仅影响新会话的沙箱。 || Omit for service defaults; applies only to new session sandboxes.
	SandboxBackend string              `json:"sandbox_backend,omitempty" enums:"sbx,gvisor,podman"`      // 省略使用服务默认。 || Omit for service defaults.
}

// ListResponse is the paginated list envelope for environments.
type ListResponse struct {
	Data       []Environment `json:"data" validate:"required"`
	NextOffset int           `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
