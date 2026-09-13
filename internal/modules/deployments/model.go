package deployments

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

type Deployment struct {
	Organization  auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner         auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID            string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID         string            `gorm:"type:uuid;index" json:"-"`
	OwnerID       string            `gorm:"type:uuid;index" json:"-"`
	Name          string            `json:"name" validate:"required"`
	AgentID       string            `json:"agent_id" validate:"required"`
	EnvironmentID string            `json:"environment_id,omitempty"`
	Input         string            `json:"input" validate:"required"`
	Cron          string            `json:"cron,omitempty"`
	Paused        bool              `json:"paused" validate:"required"`
	NextAt        *time.Time        `gorm:"index" json:"next_at,omitempty" format:"date-time"`
	LastTask      string            `json:"last_task_id,omitempty"`
	CreatedAt     time.Time         `json:"created_at" validate:"required" format:"date-time"`
}
type Run struct {
	ID           string    `gorm:"primaryKey" json:"id" validate:"required"`
	DeploymentID string    `gorm:"index" json:"deployment_id" validate:"required"`
	TaskID       string    `json:"task_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	CreatedAt    time.Time `json:"created_at" validate:"required" format:"date-time"`
}

// CreateRequest is the POST /v1/deployments request body.
type CreateRequest struct {
	Name          string `json:"name" binding:"required"`
	AgentID       string `json:"agent_id" binding:"required"`
	EnvironmentID string `json:"environment_id"`
	Input         string `json:"input" binding:"required"`
	Cron          string `json:"cron"`
}

// PauseRequest is the PATCH /v1/deployments/:id request body.
type PauseRequest struct {
	Paused *bool `json:"paused" binding:"required" extensions:"x-nullable"`
}

// ListResponse is the paginated list envelope for deployments.
type ListResponse struct {
	Data       []Deployment `json:"data" validate:"required"`
	NextOffset int          `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// RunsResponse is the paginated list envelope for deployment runs.
type RunsResponse struct {
	Data       []Run `json:"data" validate:"required"`
	NextOffset int   `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
