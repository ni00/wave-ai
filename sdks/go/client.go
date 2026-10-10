// Package wave provides Wave's complete typed API together with streaming,
// pagination, task-waiting, and file-transfer workflow methods.
package wave

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	// transport "json" only marks JSON-only output in the contract; plain
	// Call still serves it. Upload/download/SSE need their dedicated methods.
	if op.Transport != "" && op.Transport != "json" {
		return nil, fmt.Errorf("%s requires the %s API", id, op.Transport)
	}
	if id == "executionResolveToolResult" {
		if e := validateTool(o.Body); e != nil {
			return nil, e
		}
	}
	// JSON calls get a default deadline so a dead server cannot hang callers
	// that pass context.Background(); the budget covers all retry attempts.
	// Streaming methods keep the caller's full control of the deadline.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
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

// Wait blocks until the task reaches a terminal state or requires user input.
// It prefers the session's SSE stream: state-relevant events trigger an
// authoritative re-read of the task, racing a recheck timer (interval ±
// jitter) that guards against missed events. When the stream and its
// reconnects are exhausted it degrades to pure polling with exponential
// backoff + jitter, capped at interval. Cancellation is controlled entirely
// by ctx; there is no default timeout.
func (c *Client) Wait(ctx context.Context, taskID string, interval time.Duration) (*WaitResult, error) {
	if interval <= 0 {
		interval = time.Second
	}
	result, session, e := c.waitProbe(ctx, taskID, "")
	if e != nil || result != nil {
		return result, e
	}
	if result, e = c.waitStream(ctx, taskID, session, interval); e != nil || result != nil {
		return result, e
	}
	return c.waitPoll(ctx, taskID, interval)
}

// waitEventRelevant reports whether an event type can change the awaited
// task's state. Chatty progress events (deltas, spans, agent bookkeeping)
// must not trigger a re-read of the authoritative task.
func waitEventRelevant(eventType string) bool {
	switch eventType {
	case "agent.wait", "generation.failed", "generation.interrupted", "cancel.requested",
		"budget.exhausted", "input.accepted", "input.consumed":
		return true
	}
	return strings.HasPrefix(eventType, "task.") || strings.HasPrefix(eventType, "tool.")
}

// waitMaxReconnects bounds SSE reconnection attempts before Wait degrades to
// pure polling. The recheck timer keeps probing during reconnect pauses, so
// liveness never depends on the stream alone.
const waitMaxReconnects = 2

var errWaitStreamStopped = errors.New("wave: wait event stream stopped")

// waitProbe reads the authoritative task state and the session's required
// actions filtered to this task. Actions are read for waiting/unknown states,
// and unconditionally when the probe was triggered by a tool.* or agent.wait
// event: approval may be requested while the task is still marked running. A
// non-nil result means the wait is resolved; otherwise the session ID is
// returned for the event subscription. trigger is the event type that caused
// the re-read, or "" for the initial, timer, and fallback probes.
func (c *Client) waitProbe(ctx context.Context, taskID, trigger string) (*WaitResult, string, error) {
	r, e := c.Call(ctx, "executionGetTask", Options{Path: map[string]string{"id": taskID}})
	if e != nil {
		return nil, "", e
	}
	var task map[string]any
	decoder := json.NewDecoder(bytes.NewReader(r.Data))
	decoder.UseNumber()
	if e = decoder.Decode(&task); e != nil {
		return nil, "", e
	}
	result := &WaitResult{Task: task, RequiredActions: []map[string]any{}}
	state, _ := task["state"].(string)
	switch state {
	case "succeeded", "partial", "failed", "canceled":
		result.Reason = "terminal"
		return result, "", nil
	}
	session, _ := task["session_id"].(string)
	forceActions := strings.HasPrefix(trigger, "tool.") || trigger == "agent.wait"
	if state == "waiting" || state == "unknown" || forceActions {
		r, e = c.Call(ctx, "executionRequiredActions", Options{Path: map[string]string{"id": session}})
		if e != nil {
			return nil, "", e
		}
		var actions struct {
			Data []map[string]any `json:"data"`
		}
		decoder = json.NewDecoder(bytes.NewReader(r.Data))
		decoder.UseNumber()
		if e = decoder.Decode(&actions); e != nil {
			return nil, "", e
		}
		for _, a := range actions.Data {
			if a["task_id"] == taskID {
				result.RequiredActions = append(result.RequiredActions, a)
			}
		}
		if len(result.RequiredActions) > 0 || state == "unknown" {
			result.Reason = "action"
			return result, "", nil
		}
	}
	return nil, session, nil
}

// waitStream consumes the session event stream, racing state-relevant events
// against the recheck timer. It returns a resolved result, an error (ctx
// canceled or a probe failed), or nil,nil when the stream and its reconnects
// are exhausted and the caller should fall back to polling.
func (c *Client) waitStream(ctx context.Context, taskID, session string, interval time.Duration) (*WaitResult, error) {
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	// Coalesce state-relevant events into a single-slot signal carrying the
	// trigger type: the stream keeps running and its cursor keeps advancing
	// while Wait re-probes.
	relevant := make(chan string, 1)
	stopped := make(chan error, 1)
	go func() {
		stopped <- c.Events(streamCtx, session, StreamOptions{TaskID: taskID, MaxReconnects: waitMaxReconnects}, func(ev Event) error {
			if !waitEventRelevant(ev.Type) {
				return nil
			}
			select {
			case relevant <- ev.Type:
				return nil
			case <-streamCtx.Done():
				return errWaitStreamStopped
			}
		})
	}()
	timer := time.NewTimer(waitJitter(interval, 0.1))
	defer timer.Stop()
	for {
		var trigger string
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case trigger = <-relevant:
		case <-timer.C:
			timer.Reset(waitJitter(interval, 0.1))
		case <-stopped:
			// Stream ended and reconnects are exhausted; fall back to polling.
			return nil, nil
		}
		result, _, e := c.waitProbe(ctx, taskID, trigger)
		if e != nil {
			return nil, e
		}
		if result != nil {
			return result, nil
		}
	}
}

// waitPoll is the stream-less fallback: re-probe with exponential backoff and
// jitter, capped at interval, until the wait resolves or ctx ends.
func (c *Client) waitPoll(ctx context.Context, taskID string, interval time.Duration) (*WaitResult, error) {
	base := max(interval/8, time.Millisecond)
	for attempt := 0; ; attempt++ {
		result, _, e := c.waitProbe(ctx, taskID, "")
		if e != nil {
			return nil, e
		}
		if result != nil {
			return result, nil
		}
		delay := min(base*time.Duration(1<<min(attempt, 8)), interval)
		if e = pause(ctx, waitJitter(delay, 0.25)); e != nil {
			return nil, e
		}
	}
}

// waitJitter spreads d by ±frac so concurrent waiters do not poll in lockstep.
func waitJitter(d time.Duration, frac float64) time.Duration {
	spread := int64(float64(int64(d)) * frac)
	if spread <= 0 {
		return d
	}
	return time.Duration(int64(d) - spread + rand.Int64N(2*spread+1))
}

func (c *Client) Approve(ctx context.Context, task, call string, approve bool) error {
	_, e := c.Call(ctx, "executionResolveToolResult", Options{Path: map[string]string{"id": task, "call": call}, Body: map[string]any{"approve": approve}})
	return e
}
func (c *Client) SubmitResult(ctx context.Context, task, call, result string, isError bool, errorCode string) error {
	_, e := c.Call(ctx, "executionResolveToolResult", Options{Path: map[string]string{"id": task, "call": call}, Body: map[string]any{"result": result, "is_error": isError, "error_code": errorCode}})
	return e
}
