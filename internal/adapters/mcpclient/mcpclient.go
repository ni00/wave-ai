// Package mcpclient applies Wave authorization, bounds, and replay policy to the official MCP SDK.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"wave-ai.local/wave/internal/platform/egress"
)

const protocolVersion = "2025-11-25"
const responseLimit = 4 << 20

// RejectedError is a definite rejection before a tool was invoked. Other
// errors may follow external side effects and must not be replayed.
type RejectedError struct{ Message string }

func (e *RejectedError) Error() string { return e.Message }

type Result struct {
	Content           json.RawMessage `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

// Client serializes calls; different Wave sessions never share MCP sessions.
type Client struct {
	URL, Server, AuthHeader string
	HTTP                    *http.Client
	mu                      sync.Mutex
	session                 *mcp.ClientSession
}

func New(server, url, authHeader string) *Client {
	return &Client{URL: url, Server: server, AuthHeader: authHeader, HTTP: egress.GuardedClient(120*time.Second, false)}
}

type methodKey struct{}
type attemptKey struct{}
type attempt struct {
	mu      sync.Mutex
	status  int
	session bool
	err     error
}

func (a *attempt) record(status int, session bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status, a.session, a.err = status, session, err
}
func (a *attempt) snapshot() (int, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status, a.session, a.err
}

type policyTransport struct {
	base   http.RoundTripper
	bearer string
}

func (t policyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.bearer != "" {
		r.Header.Set("Authorization", t.bearer)
	}
	resp, err := t.base.RoundTrip(r)
	if r.Context().Value(methodKey{}) == "tools/call" {
		if a, ok := r.Context().Value(attemptKey{}).(*attempt); ok {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			a.record(status, r.Header.Get("Mcp-Session-Id") != "", err)
		}
	}
	if resp != nil && resp.Body != nil {
		resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: responseLimit}
	}
	return resp, err
}

type boundedBody struct {
	io.ReadCloser
	remaining int
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= n
	if b.remaining < 0 {
		return 0, errors.New("MCP response exceeds 4 MiB")
	}
	return n, err
}
func (c *Client) initialize(ctx context.Context) error {
	if c.session != nil {
		return nil
	}
	httpClient := *c.HTTP
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = policyTransport{base: base, bearer: c.AuthHeader}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	sdk := mcp.NewClient(&mcp.Implementation{Name: "wave", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	sdk.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(context.WithValue(ctx, methodKey{}, method), method, req)
		}
	})
	session, err := sdk.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: c.URL, HTTPClient: &httpClient, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: responseLimit,
	}, &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	if err != nil {
		return &RejectedError{Message: "MCP initialization failed"}
	}
	c.session = session
	return nil
}
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initialize(ctx)
}
func (c *Client) closeSession() {
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
}
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeSession()
	c.HTTP.CloseIdleConnections()
}
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := json.Marshal(args); err != nil {
		return Result{}, &RejectedError{Message: "invalid MCP arguments"}
	}
	if ctx.Err() != nil {
		return Result{}, &RejectedError{Message: "MCP request canceled before dispatch"}
	}
	for tries := 0; tries < 2; tries++ {
		if err := c.initialize(ctx); err != nil {
			return Result{}, err
		}
		a := &attempt{}
		result, err := c.session.CallTool(context.WithValue(ctx, attemptKey{}, a), &mcp.CallToolParams{Name: name, Arguments: args})
		status, hasSession, transportErr := a.snapshot()
		// Only an expired protocol session proves rejection and permits one replay.
		if status == 404 && hasSession {
			c.closeSession()
			if tries == 0 {
				continue
			}
			return Result{}, &RejectedError{Message: "MCP session expired"}
		}
		if err != nil {
			c.closeSession()
			if status == 401 || status == 403 {
				return Result{}, &RejectedError{Message: fmt.Sprintf("MCP HTTP %d", status)}
			}
			var dial *net.OpError
			var dns *net.DNSError
			if (errors.As(transportErr, &dial) && dial.Op == "dial") || errors.As(transportErr, &dns) {
				return Result{}, &RejectedError{Message: "MCP connection failed before dispatch"}
			}
			var rpc *jsonrpc.Error
			if errors.As(err, &rpc) && (status == 0 || status >= 200 && status < 300) {
				switch rpc.Code {
				case -32700, -32600, -32601, -32602:
					return Result{}, &RejectedError{Message: fmt.Sprintf("MCP RPC error %d", rpc.Code)}
				}
			}
			// SDK ErrRejected also covers timeouts after dispatch: it is not evidence
			// that a remote side effect did not happen. Never expose remote error text.
			return Result{}, errors.New("MCP tool outcome is unknown")
		}
		if result == nil || result.NeedsInput() {
			return Result{}, errors.New("MCP tool requires unsupported input; outcome is unknown")
		}
		content, err := json.Marshal(result.Content)
		if err != nil {
			return Result{}, errors.New("invalid MCP tool content")
		}
		out := Result{Content: content, IsError: result.IsError}
		if result.StructuredContent != nil {
			out.StructuredContent, err = json.Marshal(result.StructuredContent)
		}
		if err != nil {
			return Result{}, errors.New("invalid MCP structured content")
		}
		return out, nil
	}
	panic("unreachable MCP retry")
}
