# Wave AI Python SDK

English | [简体中文](README.zh-CN.md)

Python client for Wave AI with synchronous and asynchronous APIs. Requires
Python 3.10+. Install `wave-ai-client` and import `wave_ai`.

## Install

From the repository root:

```bash
python -m pip install ./sdks/python
```

## Create and wait for a task

[Start Wave](../../README.md#quick-start), set `WAVE_API_KEY`, and replace
`YOUR_MODEL` with a model supported by your provider.

```python
import os

from wave_ai import Client

with Client(api_key=os.environ["WAVE_API_KEY"]) as wave:
    agent = wave.call(
        "agentsCreate", body={"name": "assistant", "model": "YOUR_MODEL"}
    ).data
    session = wave.call("executionCreateSession", body={"title": "Demo"}).data
    task = wave.call(
        "executionCreateTask",
        path={"id": session["id"]},
        headers={"Idempotency-Key": f"first-run-{session['id']}"},
        body={"agent_id": agent["id"], "input": "Say hello"},
    ).data
    result = wave.wait(task["id"])
    print(result.reason, result.task["state"])
```

The default URL is `http://localhost:8080`; set `base_url` to change it. Configure
`timeout` and `max_retries`, or inject an `http_client`. The context manager closes
SDK-created connections; you close injected clients.

## API reference

`call` invokes JSON endpoints by their OpenAPI `operationId`. Use dedicated
methods for event streams and file transfers. The following snippets assume an
open `wave` client.

| Method | Purpose |
| --- | --- |
| `call` | Invoke one JSON operation |
| `each` | Iterate a paginated operation, yielding items |
| `events` | Iterate the session event stream |
| `wait` | Poll until the task is done or requires action |
| `approve(task, call, approve)` | Allow or deny one tool call |
| `submit_result(task, call, result, *, is_error, error_code)` | Report what a client-side tool call did |
| `download` | Stream file bytes into a binary file object |
| `upload` | Stream a multipart upload from a file object |

For typed endpoints, use `wave_ai_generated`:

```python
from wave_ai_generated import AgentsApi

with Client(api_key=os.environ["WAVE_API_KEY"]) as wave:
    with wave.raw() as raw:
        agents = AgentsApi(raw).agents_list()
```

Generated calls bypass wrapper validation, automatic pagination, and retries,
and raise subclasses of `OpenApiException`.

## Resolve required actions

When `wait` returns `reason: "terminal"`, check `task["state"]` for success.
For `reason: "action"`, handle `required_actions`:

| Action | Response |
| --- | --- |
| `approve_tool` | Call `approve` after authorizing execution; pass `False` to reject |
| `submit_tool_result` | Execute the client tool, then submit its actual result with `submit_result` |
| `confirm_tool_outcome`, `reconcile_task` | Verify external execution, then follow the [recovery workflow](../../skills/wave-client/references/recovery.md) |

Approved client tools still require a result submission. Call `wait` again after
resolving an action.

## Read events and transfer files

```python
from contextlib import closing

with closing(wave.events(session_id, max_reconnects=3)) as events:
    for event in events:
        print(event.id, event.type, event.data)
```

Reconnection is disabled by default. Set `max_reconnects` for bounded retries.
To resume, pass the session's saved `last_event_id`; do not combine it with
`after`. Deduplicate event IDs after reconnecting, and close the iterator when
leaving the loop early.

`download` writes incrementally to a binary file object; `upload` reads from a
file object. The caller closes files and uses `Content-Range` to persist HTTP 206
responses. HTTP 304 leaves the destination untouched. Interrupted transfers can
leave partial data. Neither transfer is retried automatically.

## Use the asynchronous client

`AsyncClient` provides asynchronous methods, with async generators for pagination
and event streams:

```python
from contextlib import aclosing

from wave_ai import AsyncClient

async def read_events(session_id: str):
    async with AsyncClient(api_key=os.environ["WAVE_API_KEY"]) as wave:
        async with aclosing(wave.events(session_id)) as events:
            async for event in events:
                print(event.id, event.type, event.data)
```

## Errors, retries, and timeouts

HTTP API failures raise `APIError` with `status`, `code`, `message`, and
`request_id`. Validation and transport errors retain their own types.

The client retries network errors and HTTP 429, 502, 503, and 504 up to twice.
Retries apply only to `GET` and contract-marked idempotent writes with an
`Idempotency-Key`.

| Option | Scope |
| --- | --- |
| Constructor `timeout` | HTTPX request timeout configuration, default 30 seconds; excludes retry time |
| `Client.call(timeout=...)` | Shared time budget across retries, in seconds |
| `AsyncClient.call(timeout=...)` | Request timeout per attempt; retries extend elapsed time |
| `wait(timeout=300.0, interval=1.0)` | Wait budget and polling interval, in seconds |

`wait` raises `TimeoutError` when its budget expires. An underlying request can
raise an HTTPX timeout first. A client timeout does not cancel the remote task.
Retain the task ID and reuse the submission
idempotency key on retries.

## Related documentation

[English API contract](../../api/openapi.json) · [Chinese API contract](../../api/openapi.zh-CN.json) · [CLI guide](../../cli/README.md) · [Generation and releases](../../tools/clients/README.md)
