// Package model owns the messages, tools, and usage shared by Wave execution and model adapters.
package model

// ToolDef is an OpenAI-style function tool definition.
type ToolDef struct {
	Type     string   `json:"type"` // "function"
	Function FuncSpec `json:"function"`
}

type FuncSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Message is one model conversation message.
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"` // string or []block
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall mirrors the OpenAI assistant tool_calls entry.
type ToolCall struct {
	ID       string       `json:"id" validate:"required"`
	Type     string       `json:"type" validate:"required"` // function
	Function FuncCallSpec `json:"function" validate:"required"`
}

type FuncCallSpec struct {
	Name      string `json:"name" validate:"required"`
	Arguments string `json:"arguments" validate:"required"` // JSON string
}

// Usage carries real provider numbers; pointers stay nil when unknown.
type Usage struct {
	PromptTokens     *int64 `json:"prompt_tokens" validate:"required" format:"int64" extensions:"x-nullable"`           // 供应商报告的输入 token 数；未知时为 null，不能按 0 计费。 || Provider-reported input token count; null when unknown and must not be billed as zero.
	CompletionTokens *int64 `json:"completion_tokens" validate:"required" format:"int64" extensions:"x-nullable"`       // 供应商报告的输出 token 数；未知时为 null。 || Provider-reported output token count; null when unknown.
	CacheReadTokens  *int64 `json:"cache_read_input_tokens" validate:"required" format:"int64" extensions:"x-nullable"` // 供应商报告的缓存读取 token 数；未知时为 null。 || Provider-reported cache-read token count; null when unknown.
}

// Final is the authoritative response of one model request.
type Final struct {
	Content    []ContentBlock
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string // stop | tool_calls | length | error
}

type ContentBlock struct {
	Type string `json:"type"` // text | thinking
	Text string `json:"text,omitempty"`
}

// Request is one chat completion call.
type Request struct {
	MaxOutputTokens int
	Model           string
	Effort          string
	Messages        []Message
	Tools           []ToolDef
	Stream          bool
}

// Delta is an incremental preview frame.
type Delta struct {
	Text     string // visible content delta
	Thinking string // thinking delta
}
