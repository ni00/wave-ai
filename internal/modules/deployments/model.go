package deployments

import (
	"time"

	"wave-ai.local/wave/internal/modules/execution"
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
	Version       int               `gorm:"default:1" json:"version" validate:"required"`
	AgentVersion  int               `json:"agent_version"`
	FollowLatest  bool              `json:"follow_latest"`
	Timezone      string            `json:"timezone"`
	Budget        execution.Budget  `gorm:"serializer:json;type:jsonb" json:"budget"`
	FileIDs       []string          `gorm:"serializer:json;type:jsonb" json:"file_ids,omitempty"`
	MemoryIDs     []string          `gorm:"serializer:json;type:jsonb" json:"memory_store_ids,omitempty"`
	VaultIDs      []string          `gorm:"serializer:json;type:jsonb" json:"vault_ids,omitempty"`
	MisfirePolicy string            `json:"misfire_policy"`
	OverlapPolicy string            `json:"overlap_policy"`
	PauseReason   string            `json:"pause_reason,omitempty"`
	Paused        bool              `json:"paused" validate:"required"`
	NextAt        *time.Time        `gorm:"index" json:"next_at,omitempty" format:"date-time"`
	LastTask      string            `json:"last_task_id,omitempty"`
	CreatedAt     time.Time         `json:"created_at" validate:"required" format:"date-time"`
}
type Version struct {
	DeploymentID string        `gorm:"primaryKey" json:"deployment_id"`
	Number       int           `gorm:"primaryKey;autoIncrement:false" json:"version"`
	Config       CreateRequest `gorm:"serializer:json;type:jsonb" json:"config"`
	CreatedAt    time.Time     `json:"created_at"`
}

func (Version) TableName() string { return "deployment_versions" }

type UpdateRequest struct {
	Version int           `json:"version" binding:"required"`
	Config  CreateRequest `json:"config" binding:"required"`
}
type VersionsResponse struct {
	Data       []Version `json:"data"`
	NextOffset int       `json:"next_offset,omitempty"`
}
type ScheduleResponse struct {
	Times []time.Time `json:"times"`
}

type Run struct {
	Status            string     `json:"status"`
	Trigger           string     `json:"trigger"`
	ScheduledAt       *time.Time `json:"scheduled_at,omitempty"`
	DeploymentVersion int        `json:"deployment_version"`
	AgentVersion      int        `json:"agent_version"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ID                string     `gorm:"primaryKey" json:"id" validate:"required"`
	DeploymentID      string     `gorm:"index" json:"deployment_id" validate:"required"`
	TaskID            string     `json:"task_id,omitempty"`
	SessionID         string     `json:"session_id,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	CreatedAt         time.Time  `json:"created_at" validate:"required" format:"date-time"`
}

// CreateRequest is the POST /v1/deployments request body.
type CreateRequest struct {
	Name          string           `json:"name" binding:"required"`
	AgentID       string           `json:"agent_id" binding:"required"`
	EnvironmentID string           `json:"environment_id"`
	Input         string           `json:"input" binding:"required"`
	Cron          string           `json:"cron"`
	Timezone      string           `json:"timezone"`
	AgentVersion  int              `json:"agent_version"`
	FollowLatest  bool             `json:"follow_latest"`
	Budget        execution.Budget `json:"budget"`
	FileIDs       []string         `json:"file_ids"`
	MemoryIDs     []string         `json:"memory_store_ids"`
	VaultIDs      []string         `json:"vault_ids"`
	MisfirePolicy string           `json:"misfire_policy" enums:"skip,run_once"`
	OverlapPolicy string           `json:"overlap_policy" enums:"skip,allow"`
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
