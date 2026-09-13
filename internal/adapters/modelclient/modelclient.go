// Package modelclient adapts the OpenAI-compatible chat-completions API
// to the internal streaming
// contract: incremental previews plus one authoritative final response
// with real usage. Unknown usage is reported as absent, never faked as 0.
package modelclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

// Client talks to an OpenAI-compatible endpoint.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey,
		HTTP: &http.Client{Timeout: timeout}}
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

// Stream invokes chat/completions with stream=true. It invokes onDelta for
// preview increments and returns the authoritative final response. The
// caller-supplied ctx cancels the HTTP request only.
func (c *Client) Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Final, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       req.Messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxOutputTokens > 0 {
		body["max_completion_tokens"] = req.MaxOutputTokens
	}
	if req.Effort != "" {
		body["reasoning_effort"] = req.Effort
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return nil, marshalErr
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("model api %d: %s", resp.StatusCode, string(b))
	}

	final := &Final{}
	done := false
	received := 0
	var contentBuf strings.Builder
	var thinkingBuf strings.Builder
	// streamArgs keys by the PROVIDER's tool_calls index (which is not
	// guaranteed contiguous) and remembers where each call lives in
	// final.ToolCalls, so sparse indices cannot drop arguments.
	type streamCall struct {
		pos  int
		args strings.Builder
	}
	streamArgs := map[int]*streamCall{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		received += len(data)
		if received > 16<<20 {
			return nil, fmt.Errorf("model response exceeds 16 MiB")
		}
		if data == "[DONE]" {
			done = true
			break
		}
		if data == "" {
			continue
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				// OpenAI-compatible providers send finish_reason at the
				// choice level; some put it inside delta — accept both.
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
					ToolCalls []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
					FinishReason *string `json:"finish_reason"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int64 `json:"prompt_tokens"`
				CompletionTokens    int64 `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("invalid model stream frame: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return nil, fmt.Errorf("model stream returned an error")
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if d.Content != "" {
				contentBuf.WriteString(d.Content)
				if onDelta != nil {
					onDelta(Delta{Text: d.Content})
				}
			}
			if d.Reasoning != "" {
				thinkingBuf.WriteString(d.Reasoning)
				if onDelta != nil {
					onDelta(Delta{Thinking: d.Reasoning})
				}
			}
			for _, tc := range d.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				sc := streamArgs[idx]
				if sc == nil {
					sc = &streamCall{pos: len(final.ToolCalls)}
					streamArgs[idx] = sc
					final.ToolCalls = append(final.ToolCalls, ToolCall{
						ID:       tc.ID,
						Type:     "function",
						Function: FuncCallSpec{Name: tc.Function.Name},
					})
				}
				if final.ToolCalls[sc.pos].ID == "" {
					final.ToolCalls[sc.pos].ID = tc.ID
				}
				if final.ToolCalls[sc.pos].Function.Name == "" {
					final.ToolCalls[sc.pos].Function.Name = tc.Function.Name
				}
				sc.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil {
				final.StopReason = *ch.FinishReason
			} else if d.FinishReason != nil {
				final.StopReason = *d.FinishReason
			}
		}
		if chunk.Usage != nil {
			u := chunk.Usage
			pt, ct := u.PromptTokens, u.CompletionTokens
			final.Usage.PromptTokens = &pt
			final.Usage.CompletionTokens = &ct
			if u.PromptTokensDetails != nil {
				cr := u.PromptTokensDetails.CachedTokens
				final.Usage.CacheReadTokens = &cr
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if !done && final.StopReason == "" {
		return nil, fmt.Errorf("model stream interrupted before completion")
	}
	// associate accumulated arguments back via the recorded positions
	for _, sc := range streamArgs {
		final.ToolCalls[sc.pos].Function.Arguments = sc.args.String()
	}
	seen := map[string]bool{}
	for _, call := range final.ToolCalls {
		if call.ID == "" || call.Function.Name == "" || seen[call.ID] || !json.Valid([]byte(call.Function.Arguments)) {
			return nil, fmt.Errorf("invalid model tool call")
		}
		seen[call.ID] = true
	}
	if contentBuf.Len() == 0 && len(final.ToolCalls) == 0 {
		return nil, fmt.Errorf("model returned no output")
	}
	if contentBuf.Len() > 0 {
		final.Content = append(final.Content, ContentBlock{Type: "text", Text: contentBuf.String()})
	}
	if thinkingBuf.Len() > 0 {
		final.Content = append([]ContentBlock{{Type: "thinking", Text: thinkingBuf.String()}}, final.Content...)
	}
	if final.StopReason == "" {
		final.StopReason = "stop"
	}
	return final, nil
}
