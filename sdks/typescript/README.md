# Wave AI TypeScript SDK

English | [简体中文](README.zh-CN.md)

TypeScript client for Wave AI. The `@wave-ai/client` package uses ESM and includes
type declarations.

Supports Node.js 22+ and browsers with Fetch, Streams, `AbortSignal.timeout`, and
`AbortSignal.any`. Direct browser access requires CORS configuration. Keep Wave
API keys in a trusted backend.

## Install

Build from a local checkout, then install in your application:

```bash
# From the Wave repository root.
npm ci --prefix sdks/typescript
npm run build --prefix sdks/typescript

# From your application directory; replace the checkout path.
npm install /path/to/wave-ai/sdks/typescript
```

## Create and wait for a task

[Start Wave](../../README.md#quick-start), set `WAVE_API_KEY`, and replace
`YOUR_MODEL` with a model supported by your provider. This example runs in Node.js.

```typescript
import { Client } from '@wave-ai/client';

type Created = { id: string };
const wave = new Client({ apiKey: process.env.WAVE_API_KEY });
const agent = await wave.call<Created>('agentsCreate', {
  body: { name: 'assistant', model: 'YOUR_MODEL' },
});
const session = await wave.call<Created>('executionCreateSession', {
  body: { title: 'Demo' },
});
const task = await wave.call<Created>('executionCreateTask', {
  path: { id: session.data.id },
  headers: { 'Idempotency-Key': `first-run-${session.data.id}` },
  body: { agent_id: agent.data.id, input: 'Say hello' },
});
const result = await wave.wait(task.data.id);
console.log(result.reason, result.task.state);
```

The constructor accepts `baseURL` (default `http://localhost:8080`), `apiKey`,
`fetch`, and `maxRetries` (default 2). Inject `fetch` to customize the transport.

## API reference

`call<T>` invokes JSON endpoints by their OpenAPI `operationId` and returns an
`APIResponse<T>` with `status`, `headers`, and `data`. The type parameter `T`
provides static typing; it does not validate response data.

| Method | Purpose |
| --- | --- |
| `call<T>` | Invoke one JSON operation, returning `APIResponse<T>` |
| `each<T>` | Iterate a paginated operation, yielding items |
| `events` | Iterate the session event stream as an async generator |
| `wait` | Poll until the task is done or requires action |
| `approve(task, call, approve, signal?)` | Allow or deny one tool call |
| `submitResult(task, call, result, {isError, errorCode, signal})` | Report what a client-side tool call did |
| `download` | Stream file bytes into a callback |
| `upload` | Stream a multipart upload from a `Blob` or async iterable |

For typed endpoints, use `generated`:

```typescript
import { generated } from '@wave-ai/client';

const api = new generated.AgentsApi(wave.rawConfiguration());
const agents = await api.agentsList();
```

Generated calls bypass wrapper validation, automatic pagination, retries, and
safe-integer checks.

## Resolve required actions

When `wait` returns `reason: "terminal"`, check `task.state` for success.
For `reason: "action"`, handle `required_actions`:

| Action | Response |
| --- | --- |
| `approve_tool` | Call `approve` after authorizing execution; pass `false` to reject |
| `submit_tool_result` | Execute the client tool, then submit its actual result with `submitResult` |
| `confirm_tool_outcome`, `reconcile_task` | Verify external execution, then follow the [recovery workflow](../../skills/wave-client/references/recovery.md) |

Approved client tools still require a result submission. Call `wait` again after
resolving an action.

## Read events

```typescript
const signal = AbortSignal.timeout(60_000);
for await (const event of wave.events(sessionID, { maxReconnects: 3, signal })) {
  console.log(event.id, event.type, event.data);
}
```

Stop a stream with `AbortSignal` or `break`. Set `maxReconnects` to enable
bounded reconnection (default: disabled).

To resume, pass the session's saved `lastEventID`; do not combine it with `after`.
Deduplicate event IDs after reconnecting.

## Transfer files

```typescript
const response = await wave.download('filesContent', fileID, writeChunk);
await wave.upload('filesUpload', 'report.txt', source);
```

`writeChunk` receives a `Uint8Array`, and the download awaits each callback.
`source` can be a `Blob` or an async iterable of bytes.

- HTTP 304 leaves the destination untouched.
- HTTP 206 returns a partial range; inspect the `Content-Range` response header
  before saving it.
- Interrupted transfers can leave partial data. Uploads and downloads are not
  retried automatically.

## Errors, retries, and timeouts

HTTP API failures throw `APIError` with `status`, `code`, `message`, and
`requestID`. Invalid parameters, unknown operations, or a missing request body
throw `TypeError`. JSON integers outside the safe range throw `RangeError` to
prevent silent rounding of int64 values.

The client retries network errors and HTTP 429, 502, 503, and 504 up to twice.
Retries apply only to `GET` and contract-marked idempotent writes with an
`Idempotency-Key`.

Use an `AbortSignal` to set a request deadline. `wait` defaults to a 300,000 ms
timeout and a 1,000 ms polling interval. Both `timeout` and `interval` use
milliseconds; pass `signal` to stop earlier. A client timeout does not cancel the
remote task. Keep the task ID, and reuse the idempotency key when retrying the
same submission.

## Related documentation

[English API contract](../../api/openapi.json) · [Chinese API contract](../../api/openapi.zh-CN.json) · [CLI guide](../../cli/README.md) · [Generation and releases](../../tools/clients/README.md)
