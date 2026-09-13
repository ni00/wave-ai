// Package execution owns sessions, tasks, tool results and durable events.
package execution

import (
	"time"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/auth"
)

type Session struct {
	Organization  auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner         auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID            string            `gorm:"primaryKey" json:"id" validate:"required"`
	OrgID         string            `gorm:"type:uuid;not null;index" json:"-"`
	OwnerID       string            `gorm:"type:uuid;not null;index" json:"-"`
	Archived      bool              `json:"archived" validate:"required"`
	Title         string            `json:"title" validate:"required"`
	EnvironmentID string            `json:"environment_id,omitempty"`
	ActiveRoot    string            `json:"active_task_id,omitempty"`
	WriterCall    string            `json:"-"`
	MessageSeq    int64             `json:"message_sequence" validate:"required" format:"int64"`
	EventSeq      int64             `json:"event_sequence" validate:"required" format:"int64"`
	FileIDs       []string          `gorm:"serializer:json;type:jsonb" json:"file_ids" validate:"required" extensions:"x-nullable"`
	MemoryIDs     []string          `gorm:"serializer:json;type:jsonb" json:"memory_store_ids" validate:"required" extensions:"x-nullable"`
	CreatedAt     time.Time         `json:"created_at" validate:"required" format:"date-time"`
}
type Budget struct {
	MaxConcurrentAgents int   `json:"max_concurrent_agents"`     // 根任务：<=0 使用 3；子任务固定为 0，不能再委派。 || Root tasks: <=0 uses 3. Child tasks: fixed at 0, with no further delegation.
	MaxTokens           int64 `json:"max_tokens" format:"int64"` // 根任务：<=0 使用 200000；子任务：<=0 继承父限制，正数仅可收窄。 || Root tasks: <=0 uses 200000. Child tasks: <=0 inherits the parent limit; positive values may only narrow it.
	MaxToolCalls        int   `json:"max_tool_calls"`            // 根任务：<=0 使用 200；子任务：<=0 继承父限制，正数仅可收窄。 || Root tasks: <=0 uses 200. Child tasks: <=0 inherits the parent limit; positive values may only narrow it.
	MaxAgents           int   `json:"max_agents"`                // 根任务：<=0 使用 6（含协调者）；子任务固定为 0。 || Root tasks: <=0 uses 6, including the coordinator. Child tasks: fixed at 0.
	MaxSeconds          int   `json:"max_seconds"`               // 根任务：<=0 使用 1800；子任务：<=0 继承父限制，正数仅可收窄。 || Root tasks: <=0 uses 1800. Child tasks: <=0 inherits the parent limit; positive values may only narrow it.
}

func (b Budget) Defaults() Budget {
	if b.MaxConcurrentAgents <= 0 {
		b.MaxConcurrentAgents = 3
	}
	if b.MaxTokens <= 0 {
		b.MaxTokens = 200000
	}
	if b.MaxToolCalls <= 0 {
		b.MaxToolCalls = 200
	}
	if b.MaxAgents <= 0 {
		b.MaxAgents = 6
	}
	if b.MaxSeconds <= 0 {
		b.MaxSeconds = 1800
	}
	return b
}

type Task struct {
	Session         Session                 `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE" json:"-"`
	Root            *Task                   `gorm:"foreignKey:RootID" json:"-"`
	ID              string                  `gorm:"primaryKey" json:"id" validate:"required"`
	SessionID       string                  `gorm:"not null;index" json:"session_id" validate:"required"`
	RootID          string                  `gorm:"not null;index" json:"root_id" validate:"required"`
	ParentID        string                  `gorm:"index" json:"parent_id,omitempty"`
	State           string                  `gorm:"not null;index:task_queue,priority:1" json:"state" validate:"required" enums:"queued,running,waiting,unknown,succeeded,partial,failed,canceled" example:"queued"`
	Snapshot        agents.Config           `gorm:"serializer:json;type:jsonb" json:"agent" validate:"required"`
	Experts         map[string]agents.Agent `gorm:"serializer:json;type:jsonb" json:"experts,omitempty"`
	AgentID         string                  `json:"agent_id" validate:"required"`
	AgentVersion    int                     `json:"agent_version" validate:"required"`
	Messages        []modelclient.Message   `gorm:"serializer:json;type:jsonb" json:"-"`
	Budget          Budget                  `gorm:"serializer:json;type:jsonb" json:"budget" validate:"required"`
	UsedTokens      int64                   `json:"used_tokens" validate:"required" format:"int64"`
	UsageKnown      bool                    `json:"usage_known" validate:"required"`
	ToolCount       int                     `json:"tool_calls" validate:"required"`
	AgentCount      int                     `json:"agent_count" validate:"required"`
	CancelRequested bool                    `json:"cancel_requested" validate:"required"`
	Owner           string                  `json:"-"`
	Epoch           int64                   `json:"-"`
	LeaseUntil      *time.Time              `gorm:"index" json:"-"`
	StartedAt       *time.Time              `json:"started_at,omitempty" format:"date-time"`
	FinishedAt      *time.Time              `json:"finished_at,omitempty" format:"date-time"`
	ContextReady    bool                    `json:"-"`
	Finalizing      bool                    `json:"-"`
	PendingFinish   string                  `json:"-"`
	Failures        int                     `json:"-"`
	Attempts        int                     `json:"attempts" validate:"required"`
	Result          string                  `json:"result,omitempty"`
	Error           string                  `json:"error,omitempty"`
	CreatedAt       time.Time               `gorm:"index:task_queue,priority:2" json:"created_at" validate:"required" format:"date-time"`
	UpdatedAt       time.Time               `json:"updated_at" validate:"required" format:"date-time"`
}
type Input struct {
	Task      Task      `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE" json:"-"`
	ID        string    `gorm:"primaryKey" json:"id" validate:"required"`
	TaskID    string    `gorm:"index:input_pending,priority:1" json:"task_id" validate:"required"`
	Text      string    `json:"text" validate:"required"`
	Consumed  bool      `gorm:"index:input_pending,priority:2" json:"consumed" validate:"required"`
	CreatedAt time.Time `json:"created_at" validate:"required" format:"date-time"`
}
type Event struct {
	Session   Session        `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE" json:"-"`
	SessionID string         `gorm:"primaryKey" json:"session_id" validate:"required"`
	Seq       int64          `gorm:"primaryKey;autoIncrement:false" json:"sequence" validate:"required" format:"int64"`
	TaskID    string         `gorm:"index" json:"task_id,omitempty"`
	Type      string         `json:"type" validate:"required"`
	Data      map[string]any `gorm:"serializer:json;type:jsonb" json:"data" validate:"required"`
	CreatedAt time.Time      `json:"created_at" validate:"required" format:"date-time"`
}
type ToolCall struct {
	Task       Task        `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE" json:"-"`
	ID         string      `gorm:"primaryKey" json:"id" validate:"required"`
	TaskID     string      `gorm:"index" json:"task_id" validate:"required"`
	ModelID    string      `json:"model_call_id" validate:"required"`
	Tool       agents.Tool `gorm:"serializer:json;type:jsonb" json:"tool" validate:"required"`
	Arguments  string      `json:"arguments" validate:"required"`
	Status     string      `gorm:"index" json:"status" validate:"required" enums:"ready,approval,custom,running,waiting_children,unknown,completed"` // waiting_children 表示协调者工具正在等待子任务。 || waiting_children means the coordinator tool is waiting for child tasks.
	Result     string      `json:"result,omitempty"`
	IsError    bool        `json:"is_error" validate:"required"`
	ErrorCode  string      `json:"error_code,omitempty"`
	Approval   *bool       `json:"approval,omitempty"`
	Resolution *ToolResult `gorm:"serializer:json;type:jsonb" json:"-"`
	StartedAt  *time.Time  `json:"started_at,omitempty" format:"date-time"`
	FinishedAt *time.Time  `json:"finished_at,omitempty" format:"date-time"`
	Delivered  bool        `json:"delivered" validate:"required"`
	CreatedAt  time.Time   `json:"created_at" validate:"required" format:"date-time"`
	UpdatedAt  time.Time   `json:"updated_at" validate:"required" format:"date-time"`
}

func Terminal(s string) bool {
	return s == "succeeded" || s == "partial" || s == "failed" || s == "canceled"
}
func Models() []any {
	return []any{&Session{}, &Task{}, &Input{}, &Event{}, &ToolCall{}, &Summary{}, &Receipt{}, &Message{}, &Generation{}}
}

type Workspace struct {
	SessionID          string `gorm:"primaryKey"`
	RootID             string
	PackageFingerprint string
	State              string
	UpdatedAt          time.Time
}

// CreateSessionRequest is the POST /v1/sessions request body.
type CreateSessionRequest struct {
	Title         string   `json:"title"`
	EnvironmentID string   `json:"environment_id"`
	FileIDs       []string `json:"file_ids" extensions:"x-nullable"`
	MemoryIDs     []string `json:"memory_store_ids" extensions:"x-nullable"`
}

// TaskRequest is the request body for POST /v1/sessions/:id/tasks and POST /v1/tasks/:id/agents.
type TaskRequest struct {
	AgentID string `json:"agent_id" validate:"required" example:"agent_example" minLength:"1"`
	Input   string `json:"input" validate:"required" example:"Summarize the uploaded report." minLength:"1"`
	Budget  Budget `json:"budget"`
}

// TextRequest is the request body for POST /v1/tasks/:id/inputs and POST /v1/tasks/:id/resume.
type TextRequest struct {
	Text string `json:"text" validate:"required" example:"Focus on the deployment risks." minLength:"1"`
}

// ReconcileRequest is the POST /v1/tasks/:id/reconcile request body.
type ReconcileRequest struct {
	ConfirmStopped bool `json:"confirm_stopped" validate:"required" enums:"true" example:"true"`
}

// ToolResultRequest is the POST /v1/tasks/:id/tools/:call/result request body.
// Exactly one of Result or Approve must be set; ErrorCode requires IsError.
type ToolResultRequest struct {
	Result    *string `json:"result" extensions:"x-nullable"` // 与 approve 互斥；空字符串是有效结果，null 等同未提供。 || Mutually exclusive with approve; an empty string is a valid result, while null means not provided.
	IsError   bool    `json:"is_error"`
	ErrorCode string  `json:"error_code"`                      // 仅在 is_error=true 时允许非空。 || May be nonempty only when is_error=true.
	Approve   *bool   `json:"approve" extensions:"x-nullable"` // 与 result 互斥；false 表示拒绝，null 等同未提供。 || Mutually exclusive with result; false rejects the call, while null means not provided.
}

// MessagesResponse is the cursor-paginated message history envelope.
type MessagesResponse struct {
	Data      []Message `json:"data" validate:"required"`
	NextAfter int64     `json:"next_after" validate:"required" format:"int64"`
}

// RequiredActionsResponse is the list envelope for pending user actions.
type RequiredActionsResponse struct {
	Data []RequiredAction `json:"data" validate:"required"`
}

// SessionsResponse is the paginated list envelope for sessions.
type SessionsResponse struct {
	Data       []Session `json:"data" validate:"required"`
	NextOffset int       `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// TasksResponse is the paginated list envelope for tasks.
type TasksResponse struct {
	Data       []Task `json:"data" validate:"required"`
	NextOffset int    `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// GenerationsResponse is the paginated list envelope for model generations.
type GenerationsResponse struct {
	Data       []Generation `json:"data" validate:"required"`
	NextOffset int          `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// InputsResponse is the paginated list envelope for task inputs.
type InputsResponse struct {
	Data       []Input `json:"data" validate:"required"`
	NextOffset int     `json:"next_offset,omitempty"` // 仅有下一页时返回；省略表示末页。 || Returned only when another page exists; omitted on the last page.
}

// ToolCallsResponse is the list envelope for task tool calls.
type ToolCallsResponse struct {
	Data []ToolCall `json:"data" validate:"required"`
}

// EventsResponse is the list envelope for session events.
type EventsResponse struct {
	Data []Event `json:"data" validate:"required"`
}

// Narrow applies only explicit smaller limits; zero inherits the parent limit.
func (b Budget) Narrow(child Budget) Budget {
	if child.MaxTokens > 0 {
		b.MaxTokens = min(b.MaxTokens, child.MaxTokens)
	}
	if child.MaxToolCalls > 0 {
		b.MaxToolCalls = min(b.MaxToolCalls, child.MaxToolCalls)
	}
	if child.MaxSeconds > 0 {
		b.MaxSeconds = min(b.MaxSeconds, child.MaxSeconds)
	}
	b.MaxAgents = 0
	b.MaxConcurrentAgents = 0
	return b
}
