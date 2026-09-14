# Wave AI Go SDK

English | [简体中文](README.zh-CN.md)

Go client for Wave AI. Module: `github.com/ni00/wave-ai/sdks/go`; package: `wave`.
Requires Go 1.23+.

## Install

Add the local SDK to your application's `go.mod`. Replace the path with your
checkout location:

```go
require github.com/ni00/wave-ai/sdks/go v0.0.0

replace github.com/ni00/wave-ai/sdks/go => /path/to/wave-ai/sdks/go
```

## Create and wait for a task

[Start Wave](../../README.md#quick-start), set `WAVE_API_KEY`, and replace
`YOUR_MODEL` with a model supported by your provider.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	wave "github.com/ni00/wave-ai/sdks/go"
)

func main() {
	client, err := wave.New("http://localhost:8080", os.Getenv("WAVE_API_KEY"))
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	agent := create(ctx, client, "agentsCreate", nil,
		map[string]any{"name": "assistant", "model": "YOUR_MODEL"})
	session := create(ctx, client, "executionCreateSession", nil,
		map[string]any{"title": "demo"})
	task := create(ctx, client, "executionCreateTask", map[string]string{"id": session},
		map[string]any{"agent_id": agent, "input": "Say hello"})

	result, err := client.Wait(ctx, task, 2*time.Second)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Reason, result.Task["state"])
}

func create(ctx context.Context, c *wave.Client, op string, path map[string]string, body map[string]any) string {
	res, err := c.Call(ctx, op, wave.Options{Path: path, Body: body})
	if err != nil {
		panic(err)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		panic(err)
	}
	return out.ID
}
```

`New` defaults to `http://localhost:8080` and accepts HTTP(S) URLs without
credentials, a query, or a fragment. Set `HTTPClient` to customize the transport
or proxy, and `MaxRetries` to change the retry limit.

## Call the API

`Call` invokes JSON endpoints by their OpenAPI `operationId`. `Options` accepts
path values, query parameters, headers, and a request body. Use dedicated methods
for uploads, downloads, and event streams.

For typed endpoints, use the generated client:

```go
agents, response, err := client.Raw().AgentsAPI.AgentsList(ctx).Execute()
```

Generated calls bypass wrapper validation, automatic pagination, and retries.

### Method reference

| Method | Purpose |
| --- | --- |
| `Call` | Invoke one JSON operation, returning `*Response` |
| `Each` | Iterate a paginated operation, one `json.RawMessage` per item |
| `Wait` | Poll until the task reaches a terminal state or requires action |
| `Approve(ctx, task, call, approve)` | Allow or deny one tool call |
| `SubmitResult(ctx, task, call, result, isError, errorCode)` | Submit a client-executed tool result |
| `Events` | Consume the session event stream |
| `Download` | Stream file bytes into an `io.Writer` |
| `Upload` | Stream a multipart upload from an `io.Reader` |

## Resolve required actions

When `Wait` returns `Reason: "terminal"`, check `Task["state"]` for success.
For `Reason: "action"`, handle `RequiredActions`:

| Action | Response |
| --- | --- |
| `approve_tool` | Call `Approve` after authorizing execution; pass `false` to reject |
| `submit_tool_result` | Execute the client tool, then submit its actual result with `SubmitResult` |
| `confirm_tool_outcome`, `reconcile_task` | Verify external execution, then follow the [recovery workflow](../../skills/wave-client/references/recovery.md) |

Approved client tools still require a result submission. Call `Wait` again after
resolving an action.

## Read events

```go
err = client.Events(ctx, sessionID,
	wave.StreamOptions{LastEventID: savedCursor, MaxReconnects: 3},
	func(event wave.Event) error {
		fmt.Println(event.ID, event.Type, event.Data)
		return nil
	})
```

`savedCursor` is the last event ID saved for this session; use an empty string
for the first read. Set `MaxReconnects` to enable bounded reconnection (default:
disabled). Cancel `ctx` or return an error from the callback to stop reading.

`After` and `LastEventID` are mutually exclusive. Deduplicate event IDs
after reconnecting.

## Transfer files

`Download` streams into an `io.Writer`; `Upload` streams multipart data from an
`io.Reader`. The caller closes files and decides how to persist results.

- HTTP 304 leaves the destination untouched.
- HTTP 206 returns a partial range; inspect `Content-Range` before saving it.
- Interrupted transfers can leave partial data. Uploads and downloads are not
  retried automatically.

## Errors, retries, and timeouts

HTTP API failures return `*wave.APIError` with `StatusCode`, `Code`, `Message`,
and `RequestID`. Validation, transport, and context errors retain their own types.

The client retries network errors and HTTP 429, 502, 503, and 504 up to twice.
Retries apply only to `GET` and contract-marked idempotent writes with an
`Idempotency-Key`.

Set request, retry, and wait deadlines with `context`; there is no default overall
timeout. A client timeout does not cancel the remote task.

## Related documentation

[English API contract](../../api/openapi.json) · [Chinese API contract](../../api/openapi.zh-CN.json) · [CLI guide](../../cli/README.md) · [Generation and releases](../../tools/clients/README.md)
