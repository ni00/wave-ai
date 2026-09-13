// Package wave provides Wave's streaming and workflow APIs alongside the
// complete generated API exposed by Client.Raw.
package wave

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ni00/wave-ai/sdks/go/generated"
)

//go:embed operations.json
var operationJSON []byte

//go:embed openapi.json
var openapiJSON []byte

func Specification() json.RawMessage { return append(json.RawMessage(nil), openapiJSON...) }

type Parameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Required    bool           `json:"required"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}
type Pagination struct {
	Request    string `json:"request"`
	Next       string `json:"next"`
	ItemCursor string `json:"itemCursor"`
}
type Operation struct {
	ID            string         `json:"id"`
	Command       string         `json:"command"`
	Method        string         `json:"method"`
	Path          string         `json:"path"`
	Summary       string         `json:"summary"`
	Parameters    []Parameter    `json:"parameters"`
	RequestBody   map[string]any `json:"requestBody"`
	Responses     map[string]any `json:"responses"`
	Authenticated bool           `json:"authenticated"`
	Idempotent    bool           `json:"idempotent"`
	Transport     string         `json:"transport"`
	Pagination    *Pagination    `json:"pagination"`
}

func Operations() map[string]Operation {
	var v map[string]Operation
	if e := json.Unmarshal(operationJSON, &v); e != nil {
		panic(e)
	}
	return v
}

var operationRegistry = Operations()

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	MaxRetries int
}

func New(baseURL, key string) (*Client, error) {
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	u, e := url.Parse(baseURL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	key = strings.TrimSpace(key)
	if strings.HasPrefix(strings.ToLower(key), "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: key, HTTPClient: &http.Client{}, MaxRetries: 2}, nil
}

// Raw exposes every typed generated endpoint. Its methods have generator-level
// semantics; use this package's Events and Download for streaming.
func (c *Client) Raw() *generated.APIClient {
	cfg := generated.NewConfiguration()
	cfg.Servers = generated.ServerConfigurations{{URL: c.BaseURL}}
	cfg.HTTPClient = c.httpClient()
	if c.APIKey != "" {
		cfg.DefaultHeader["Authorization"] = "Bearer " + c.APIKey
	}
	return generated.NewAPIClient(cfg)
}
func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

type Options struct {
	Path    map[string]string
	Query   url.Values
	Headers http.Header
	Body    any
}
type Response struct {
	StatusCode int
	Header     http.Header
	Data       json.RawMessage
}
type APIError struct {
	StatusCode int    `json:"status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id,omitempty"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("wave: HTTP %d %s: %s (request_id=%s)", e.StatusCode, e.Code, e.Message, e.RequestID)
}
func decodeError(r *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var envelope struct {
		Error struct {
			Code    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(data, &envelope)
	if envelope.Error.Message == "" {
		envelope.Error.Message = strings.TrimSpace(string(data))
	}
	if envelope.Error.Message == "" {
		envelope.Error.Message = http.StatusText(r.StatusCode)
	}
	if envelope.RequestID == "" {
		envelope.RequestID = r.Header.Get("X-Request-ID")
	}
	return &APIError{r.StatusCode, envelope.Error.Code, envelope.Error.Message, envelope.RequestID}
}

func (c *Client) request(ctx context.Context, op Operation, o Options, body io.Reader, contentType string) (*http.Request, error) {
	path := op.Path
	for _, p := range op.Parameters {
		if p.In == "path" {
			v := o.Path[p.Name]
			if v == "" {
				return nil, fmt.Errorf("missing path parameter %s", p.Name)
			}
			if v == "." || v == ".." {
				return nil, fmt.Errorf("invalid path parameter %s", p.Name)
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(v))
		}
	}
	req, e := http.NewRequestWithContext(ctx, op.Method, c.BaseURL+path, body)
	if e != nil {
		return nil, e
	}
	req.URL.RawQuery = o.Query.Encode()
	req.Header = o.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if op.Authenticated && c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	req.Header.Set("User-Agent", "wave-go/0.1.0")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func retryDelay(r *http.Response, attempt int) time.Duration {
	d := time.Duration(1<<min(attempt, 5)) * 200 * time.Millisecond
	if r != nil {
		if n, e := strconv.Atoi(r.Header.Get("Retry-After")); e == nil && n >= 0 {
			d = time.Duration(n) * time.Second
		} else if t, e := http.ParseTime(r.Header.Get("Retry-After")); e == nil {
			d = time.Until(t)
		}
	}
	return max(0, min(d, 30*time.Second))
}
func retryStatus(status int) bool {
	return status == 429 || status == 502 || status == 503 || status == 504
}
func validateTool(body any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		return e
	}
	a, r := m["approve"], m["result"]
	if (a == nil) == (r == nil) {
		return errors.New("submit exactly one non-null approve or result")
	}
	if a != nil {
		if _, ok := a.(bool); !ok {
			return errors.New("approve must be a boolean")
		}
		if m["is_error"] == true || (m["error_code"] != nil && m["error_code"] != "") {
			return errors.New("approval cannot include error details")
		}
	}
	if r != nil {
		if _, ok := r.(string); !ok {
			return errors.New("result must be a string")
		}
	}
	if m["error_code"] != nil && m["error_code"] != "" && m["is_error"] != true {
		return errors.New("error_code requires is_error=true")
	}
	return nil
}

// Call invokes any JSON operation by stable operationId. Binary and SSE
// operations intentionally require the streaming methods below.
func (c *Client) Call(ctx context.Context, id string, o Options) (*Response, error) {
	op, ok := operationRegistry[id]
	if !ok {
		return nil, fmt.Errorf("unknown operation %s", id)
	}
	if op.Transport != "" {
		return nil, fmt.Errorf("%s requires the %s API", id, op.Transport)
	}
	if id == "executionResolveToolResult" {
		if e := validateTool(o.Body); e != nil {
			return nil, e
		}
	}
	var data []byte
	var e error
	contentType := ""
	if o.Body != nil {
		data, e = json.Marshal(o.Body)
		if e != nil {
			return nil, e
		}
		contentType = "application/json"
	}
	if op.RequestBody["required"] == true && o.Body == nil {
		return nil, errors.New("request body is required")
	}
	safe := op.Method == "GET" || (op.Idempotent && o.Headers.Get("Idempotency-Key") != "")
	for attempt := 0; ; attempt++ {
		req, e := c.request(ctx, op, o, bytes.NewReader(data), contentType)
		if e != nil {
			return nil, e
		}
		r, e := c.httpClient().Do(req)
		if safe && attempt < c.MaxRetries && (e != nil || retryStatus(r.StatusCode)) && ctx.Err() == nil {
			d := retryDelay(r, attempt)
			if r != nil {
				io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
				r.Body.Close()
			}
			if e = pause(ctx, d); e != nil {
				return nil, e
			}
			continue
		}
		if e != nil {
			return nil, e
		}
		defer r.Body.Close()
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			return nil, decodeError(r)
		}
		b, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		if len(b) > 0 && !json.Valid(b) {
			return nil, errors.New("expected a JSON response")
		}
		return &Response{r.StatusCode, r.Header, b}, nil
	}
}

// Each iterates all pages and stops if the server returns a non-advancing cursor.
func (c *Client) Each(ctx context.Context, id string, o Options, visit func(json.RawMessage) error) error {
	op, ok := operationRegistry[id]
	if !ok || op.Pagination == nil {
		return fmt.Errorf("operation %s has no pagination", id)
	}
	q := make(url.Values)
	for k, v := range o.Query {
		q[k] = append([]string(nil), v...)
	}
	o.Query = q
	cursor := q.Get(op.Pagination.Request)
	if cursor == "" {
		cursor = o.Headers.Get("Last-Event-ID")
	}
	if cursor == "" {
		cursor = "0"
	}
	for {
		q.Set(op.Pagination.Request, cursor)
		r, e := c.Call(ctx, id, o)
		if e != nil {
			return e
		}
		var page map[string]json.RawMessage
		if e = json.Unmarshal(r.Data, &page); e != nil {
			return e
		}
		var items []json.RawMessage
		if e = json.Unmarshal(page["data"], &items); e != nil {
			return e
		}
		if len(items) == 0 {
			return nil
		}
		for _, item := range items {
			if e = visit(item); e != nil {
				return e
			}
		}
		next := page[op.Pagination.Next]
		if op.Pagination.ItemCursor != "" {
			var last map[string]json.RawMessage
			_ = json.Unmarshal(items[len(items)-1], &last)
			next = last[op.Pagination.ItemCursor]
		}
		if len(next) == 0 || string(next) == "null" {
			return nil
		}
		n, e := strconv.ParseInt(string(next), 10, 64)
		if e != nil {
			return fmt.Errorf("invalid pagination cursor: %s", next)
		}
		old, _ := strconv.ParseInt(cursor, 10, 64)
		if n <= old {
			return errors.New("pagination cursor did not advance")
		}
		cursor = strconv.FormatInt(n, 10)
	}
}

type WaitResult struct {
	Task            map[string]any   `json:"task"`
	RequiredActions []map[string]any `json:"required_actions"`
	Reason          string           `json:"reason"`
}

func (c *Client) Wait(ctx context.Context, taskID string, interval time.Duration) (*WaitResult, error) {
	if interval <= 0 {
		interval = time.Second
	}
	for {
		r, e := c.Call(ctx, "executionGetTask", Options{Path: map[string]string{"id": taskID}})
		if e != nil {
			return nil, e
		}
		var task map[string]any
		decoder := json.NewDecoder(bytes.NewReader(r.Data))
		decoder.UseNumber()
		if e = decoder.Decode(&task); e != nil {
			return nil, e
		}
		result := &WaitResult{Task: task, RequiredActions: []map[string]any{}}
		switch task["state"] {
		case "succeeded", "partial", "failed", "canceled":
			result.Reason = "terminal"
			return result, nil
		}
		session, _ := task["session_id"].(string)
		r, e = c.Call(ctx, "executionRequiredActions", Options{Path: map[string]string{"id": session}})
		if e != nil {
			return nil, e
		}
		var actions struct {
			Data []map[string]any `json:"data"`
		}
		decoder = json.NewDecoder(bytes.NewReader(r.Data))
		decoder.UseNumber()
		if e = decoder.Decode(&actions); e != nil {
			return nil, e
		}
		for _, a := range actions.Data {
			if a["task_id"] == taskID {
				result.RequiredActions = append(result.RequiredActions, a)
			}
		}
		if len(result.RequiredActions) > 0 || task["state"] == "unknown" {
			result.Reason = "action"
			return result, nil
		}
		if e = pause(ctx, interval); e != nil {
			return nil, e
		}
	}
}

func (c *Client) Approve(ctx context.Context, task, call string, approve bool) error {
	_, e := c.Call(ctx, "executionResolveToolResult", Options{Path: map[string]string{"id": task, "call": call}, Body: map[string]any{"approve": approve}})
	return e
}
func (c *Client) SubmitResult(ctx context.Context, task, call, result string, isError bool, errorCode string) error {
	_, e := c.Call(ctx, "executionResolveToolResult", Options{Path: map[string]string{"id": task, "call": call}, Body: map[string]any{"result": result, "is_error": isError, "error_code": errorCode}})
	return e
}
