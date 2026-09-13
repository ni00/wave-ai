# Wave AI Go client

[简体中文](README.zh-CN.md) | English

Module: `github.com/ni00/wave-ai/sdks/go`, Go 1.23+. Before the first public
module tag exists, use a local `replace` pointing to this directory.

```go
client, err := wave.New("http://localhost:8080", os.Getenv("WAVE_API_KEY"))
if err != nil { return err }
ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
defer cancel()
agents, _, err := client.Raw().AgentsAPI.AgentsList(ctx).Execute()
```

Import this package as `wave`, and generated types from its `/generated` subpackage.
The full typed API is accessible through `Raw`. `Call` invokes a JSON operation by
stable operationId with `Options{Path, Query, Headers, Body}`. Raw endpoints use
generator semantics; public helpers add validation, bounded safe retries and workflows.

- `Each(ctx, operation, options, visit)` iterates paginated items.
- `Events(ctx, session, StreamOptions{LastEventID:"42"}, visit)` streams complete
  events. Cancel ctx or return an error to stop. Reconnect is opt-in and bounded.
- `Wait(ctx, taskID, interval)` returns `terminal` or `action`, filtered to that task.
- `Approve(..., false)` and `SubmitResult(..., "", false, "")` preserve explicit values.
- `Download(ctx, "filesContent", id, headers, writer)` streams bytes; 304 leaves the
  writer untouched. On 206 inspect Content-Range before choosing how to store the range.
- `Upload(ctx, "filesUpload", filename, reader, fields)` streams multipart data.

Caller owns readers/writers and controls deadlines using context. File transfers
are not automatically replayed. `APIError` includes status, code and request ID.
`Operations()` and `Specification()` expose the generated command map and contract.

API documentation is available in [Chinese](../../api/openapi.zh-CN.json) and [English](../../api/openapi.json), with identical contract structure.
See the [CLI guide](../../cli/README.md) and [generation and release guide](../../tools/clients/README.md).
