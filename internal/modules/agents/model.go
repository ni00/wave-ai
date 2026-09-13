package agents

import (
	"time"

	"wave-ai.local/wave/internal/platform/auth"
)

// Tool is an explicitly authorized capability. The platform dispatches only
// definitions in the task snapshot, never a model-supplied URL or credential.
type Tool struct {
	Name         string         `json:"name" binding:"required" example:"lookup"`
	Kind         string         `json:"kind" binding:"required" enums:"builtin,mcp,custom" example:"custom"` // builtin, mcp, custom
	Description  string         `json:"description"`
	Parameters   map[string]any `json:"parameters" extensions:"x-nullable"` // 工具参数的 JSON Schema；kind=custom 时必须提供非 null 对象。 || JSON Schema for tool parameters; kind=custom requires a non-null object.
	Approval     bool           `json:"approval"`
	ServerURL    string         `json:"server_url,omitempty"` // kind=mcp 时必须提供 HTTPS URL，禁止内嵌用户名和密码。 || When kind=mcp, an HTTPS URL is required; embedded usernames and passwords are forbidden.
	RemoteName   string         `json:"remote_name,omitempty"`
	CredentialID string         `json:"credential_id,omitempty"`
}
type Config struct {
	Name         string   `json:"name" binding:"required" example:"Research assistant"`
	Model        string   `json:"model" binding:"required" example:"your-model-id"`
	Instructions string   `json:"instructions" example:"Answer clearly and cite your sources."`
	Effort       string   `json:"effort,omitempty"`
	Tools        []Tool   `json:"tools" extensions:"x-nullable"`
	SkillIDs     []string `json:"skill_ids" extensions:"x-nullable"`
	ExpertIDs    []string `json:"expert_ids" extensions:"x-nullable"`
}
type Agent struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID        string            `gorm:"type:uuid;not null;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;not null;index" json:"-"`
	Version      int               `json:"version" validate:"required"`
	Config       Config            `gorm:"serializer:json;type:jsonb" json:"config" validate:"required"`
	Archived     bool              `json:"archived" validate:"required"`
	CreatedAt    time.Time         `json:"created_at" validate:"required" format:"date-time"`
	UpdatedAt    time.Time         `json:"updated_at" validate:"required" format:"date-time"`
}
type Version struct {
	AgentID   string    `gorm:"primaryKey" json:"agent_id" validate:"required"`
	Number    int       `gorm:"primaryKey;autoIncrement:false" json:"version" validate:"required"`
	Config    Config    `gorm:"serializer:json;type:jsonb" json:"config" validate:"required"`
	CreatedAt time.Time `json:"created_at" validate:"required" format:"date-time"`
}

func (Version) TableName() string { return "agent_versions" }

// UpdateRequest is the PUT /v1/agents/:id request body.
type UpdateRequest struct {
	Version int    `json:"version" validate:"required" minimum:"1" example:"1"`
	Config  Config `json:"config" validate:"required"`
}

// ListResponse is the paginated list envelope for agents.
type ListResponse struct {
	Data       []Agent `json:"data" validate:"required"`
	NextOffset int     `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// VersionsResponse is the paginated list envelope for agent versions.
type VersionsResponse struct {
	Data       []Version `json:"data" validate:"required"`
	NextOffset int       `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}
