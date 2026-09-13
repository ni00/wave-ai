// Package mcpclient implements bounded, reusable MCP Streamable HTTP sessions.
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wave-ai.local/wave/internal/platform/egress"
)

const protocolVersion = "2025-11-25"
const responseLimit = 4 << 20

var requestSequence atomic.Uint64
var errSessionExpired = errors.New("MCP session expired")

// RejectedError is a definite rejection before a tool was invoked. Other
// errors may follow external side effects and must not be replayed.
type RejectedError struct{ Message string }

func (e *RejectedError) Error() string { return e.Message }

type Result struct {
	Content           json.RawMessage `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

// A connection serializes its handshake and calls; separate user sessions never
// share an MCP protocol session, even when their URLs and credentials match.
type Client struct {
	URL        string
	Server     string
	HTTP       *http.Client
	AuthHeader string
	mu         sync.Mutex
	sessionID  string
	ready      bool
}

func New(server, url, authHeader string) *Client {
	httpClient := egress.GuardedClient(120*time.Second, false)
	// Protocol session and authorization headers belong to this exact endpoint.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{URL: url, Server: server, AuthHeader: authHeader, HTTP: httpClient}
}

func (c *Client) post(ctx context.Context, method string, params, result any) error {
	id := strconv.FormatUint(requestSequence.Add(1), 10)
	notification := strings.HasPrefix(method, "notifications/")
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if !notification {
		payload["id"] = id
	}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return &RejectedError{Message: "invalid MCP request"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(raw))
	if err != nil {
		return &RejectedError{Message: "invalid MCP endpoint"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.AuthHeader != "" {
		req.Header.Set("Authorization", c.AuthHeader)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	if method != "initialize" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	if err := ctx.Err(); err != nil {
		return &RejectedError{Message: "MCP request canceled before dispatch"}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var dial *net.OpError
		var dns *net.DNSError
		if (errors.As(err, &dial) && dial.Op == "dial") || errors.As(err, &dns) {
			return &RejectedError{Message: "MCP connection failed before dispatch"}
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && c.sessionID != "" {
		return errSessionExpired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not reflect server response bodies, which can contain bearer credentials.
		message := fmt.Sprintf("MCP HTTP %d", resp.StatusCode)
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return &RejectedError{Message: message}
		}
		return errors.New(message)
	}
	if notification {
		return nil
	}
	if method == "initialize" {
		c.sessionID = resp.Header.Get("Mcp-Session-Id")
	}
	reader := io.LimitReader(resp.Body, responseLimit+1)
	decode := func(raw []byte) (bool, error) {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return false, errors.New("invalid MCP response")
		}
		expected, _ := json.Marshal(id)
		if !bytes.Equal(envelope.ID, expected) {
			return false, nil
		}
		if envelope.Error != nil {
			msg := fmt.Sprintf("MCP RPC error %d", envelope.Error.Code)
			switch envelope.Error.Code {
			case -32700, -32600, -32601, -32602:
				return true, &RejectedError{Message: msg}
			}
			return true, errors.New(msg)
		}
		if len(envelope.Result) == 0 {
			return true, errors.New("MCP response has no result")
		}
		return true, json.Unmarshal(envelope.Result, result)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), responseLimit)
		var event strings.Builder
		consumed := 0
		for scanner.Scan() {
			line := scanner.Text()
			consumed += len(line) + 1
			if consumed > responseLimit {
				return errors.New("MCP response exceeds 4 MiB")
			}
			if line == "" {
				if event.Len() > 0 {
					found, e := decode([]byte(event.String()))
					if e != nil {
						return e
					}
					if found {
						return nil
					}
					event.Reset()
				}
			} else if value, ok := strings.CutPrefix(line, "data:"); ok {
				event.WriteString(strings.TrimPrefix(value, " "))
				event.WriteByte('\n')
			}
		}
		if scanner.Err() != nil {
			return scanner.Err()
		}
		return errors.New("MCP stream interrupted before a matching result")
	}
	raw, err = io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(raw) > responseLimit {
		return errors.New("MCP response exceeds 4 MiB")
	}
	found, err := decode(raw)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("MCP response ID mismatch")
	}
	return nil
}

func (c *Client) initialize(ctx context.Context) error {
	if c.ready {
		return nil
	}
	c.sessionID = ""
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.post(ctx, "initialize", map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "wave", "version": "1"}}, &result); err != nil {
		return err
	}
	if result.ProtocolVersion != protocolVersion {
		return errors.New("unsupported MCP protocol version")
	}
	if err := c.post(ctx, "notifications/initialized", nil, nil); err != nil {
		return err
	}
	// Only explicitly configured tools are exposed to the model. Initialization
	// does not need to download an entire server catalog on every connection.
	c.ready = true
	return nil
}
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initialize(ctx)
}
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result Result
	if err := c.initialize(ctx); err != nil {
		return result, &RejectedError{Message: "MCP initialization failed: " + err.Error()}
	}
	err := c.post(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &result)
	// The protocol explicitly rejects expired session IDs before invocation. Only
	// this rejection permits one new handshake and retry; timeouts never do.
	if errors.Is(err, errSessionExpired) {
		c.ready = false
		if err = c.initialize(ctx); err != nil {
			return result, &RejectedError{Message: "MCP initialization failed: " + err.Error()}
		}
		err = c.post(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &result)
	}
	if err != nil {
		c.ready = false
		c.HTTP.CloseIdleConnections()
	}
	return result, err
}
func (c *Client) Close() { c.HTTP.CloseIdleConnections() }
