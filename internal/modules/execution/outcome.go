package execution

// ToolResult is a confirmed outcome. Executor errors mean the outcome could
// not be established and must never trigger an automatic replay.
type ToolResult struct {
	Output    string `json:"result"`
	IsError   bool   `json:"is_error"`
	ErrorCode string `json:"error_code,omitempty"`
}

func ToolFailed(code, message string) ToolResult {
	return ToolResult{Output: message, IsError: true, ErrorCode: code}
}

func (c ToolCall) Outcome() string {
	if c.Status == "unknown" {
		return "unknown"
	}
	if c.Status != "completed" {
		return "pending"
	}
	if c.IsError {
		return "failed"
	}
	return "succeeded"
}
