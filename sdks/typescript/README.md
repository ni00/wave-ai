# Wave AI TypeScript client

[简体中文](README.zh-CN.md) | English

Node.js 22+ and modern browsers with Fetch, streams and AbortSignal.timeout/any.
Build with `npm ci && npm run build`. This package ships native ESM and declarations.

```typescript
import { Client, generated } from '@wave-ai/client';
const wave = new Client({apiKey: process.env.WAVE_API_KEY});
const api = new generated.AgentsApi(wave.rawConfiguration());
const agents = await api.agentsList();
for await (const event of wave.events('session_id', {lastEventID: '42'})) {
  console.log(event.id, event.data);
}
```

`call(operationId, options)` covers JSON endpoints; generated APIs provide full
model typing. `each` iterates pages, `wait` returns on terminal state or required
action, `approve(..., false)` and `submitResult(..., '')` preserve explicit values.
Pass AbortSignal to cancel requests and streams; breaking event iteration closes
the response. Bounded reconnection is opt-in through `maxReconnects`.

`download('filesContent', id, writeChunk)` awaits each write, preserves 206 response
metadata and writes nothing on 304. `upload('filesUpload', name, blobOrAsyncIterable)`
supports browser Blobs and Node streaming sources. Browser cross-origin requests
require a deployment with suitable CORS configuration; server keys belong in a
trusted backend, not a publicly distributed frontend bundle.

The public client rejects unsafe integer JSON values instead of silently rounding
int64 counters. Typed generated methods retain the generator's native number and
transport behavior. See the [CLI guide](../../cli/README.md) and [generation and release guide](../../tools/clients/README.md).

API documentation is available in [Chinese](../../api/openapi.zh-CN.json) and [English](../../api/openapi.json), with identical contract structure.
