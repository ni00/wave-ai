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

	"wave-ai.local/wave/internal/platform/model"
)

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

// Stream invokes chat/completions with stream=true. It invokes onDelta for
// preview increments and returns the authoritative final response. The
// caller-supplied ctx cancels the HTTP request only.
func (c *Client) Stream(ctx context.Context, req model.Request, onDelta func(model.Delta)) (*model.Final, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       req.Messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxOutputTokens > 0 {
		// Providers disagree on the field name (OpenAI: max_completion_tokens,
		// DeepSeek: max_tokens). Sending both is accepted by each and the
		// stricter value wins where both are honored.
		body["max_completion_tokens"] = req.MaxOutputTokens
		body["max_tokens"] = req.MaxOutputTokens
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
		return nil, fmt.Errorf("model api %d: %s", resp.StatusCode, errorDetail(b))
	}

	final := &model.Final{}
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
					onDelta(model.Delta{Text: d.Content})
				}
			}
			if d.Reasoning != "" {
				thinkingBuf.WriteString(d.Reasoning)
				if onDelta != nil {
					onDelta(model.Delta{Thinking: d.Reasoning})
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
					final.ToolCalls = append(final.ToolCalls, model.ToolCall{
						ID:       tc.ID,
						Type:     "function",
						Function: model.FuncCallSpec{Name: tc.Function.Name},
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
	for i, call := range final.ToolCalls {
		if call.ID == "" || call.Function.Name == "" || seen[call.ID] {
			return nil, fmt.Errorf("invalid model tool call")
		}
		// No-argument tools legitimately stream empty arguments; normalize
		// instead of rejecting, which would fail the whole model turn.
		if call.Function.Arguments == "" {
			final.ToolCalls[i].Function.Arguments = "{}"
			call.Function.Arguments = "{}"
		}
		if !json.Valid([]byte(call.Function.Arguments)) {
			return nil, fmt.Errorf("invalid model tool call")
		}
		seen[call.ID] = true
	}
	if contentBuf.Len() == 0 && len(final.ToolCalls) == 0 {
		return nil, fmt.Errorf("model returned no output")
	}
	if contentBuf.Len() > 0 {
		final.Content = append(final.Content, model.ContentBlock{Type: "text", Text: contentBuf.String()})
	}
	if thinkingBuf.Len() > 0 {
		final.Content = append([]model.ContentBlock{{Type: "thinking", Text: thinkingBuf.String()}}, final.Content...)
	}
	if final.StopReason == "" {
		final.StopReason = "stop"
	}
	return final, nil
}

// errorDetail extracts the provider's structured error message. The raw
// body is never returned: it is persisted as the generation/task error and
// exposed through the API and SSE, and can echo credentials, account IDs or
// internal endpoint details that the provider page otherwise includes.
func errorDetail(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || strings.TrimSpace(envelope.Error.Message) == "" {
		return ""
	}
	message := strings.TrimSpace(envelope.Error.Message)
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
