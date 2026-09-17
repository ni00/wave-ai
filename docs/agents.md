# Configure agents

English | [简体中文](agents.zh-CN.md)

Configure agent memory, scheduled runs, expert delegation, credentials,
webhooks, and output acceptance checks.

## Agent memory

### Enable memory

1. Create a memory store through the API or [CLI](../cli/README.md).
2. Bind its ID with `memory_store_ids` when creating a session. Bind the same store to other sessions that need the saved information. No environment is required.
3. Add the built-in tools `memory_read`, `memory_write`, and `memory_delete` to the agent's tool list. Enable only the operations the agent needs.

Tools can access only stores owned by the caller and bound to the current session.

### Read and update entries

| Tool | Required arguments | Behavior |
| --- | --- | --- |
| `memory_read` | `store_id` | Omit `path` to list entries; provide `path` to read one entry. |
| `memory_write` | `store_id`, `path`, `content`, `version` | Save content if the version matches. |
| `memory_delete` | `store_id`, `path`, `version` | Mark an entry as deleted if the version matches. |

Paths must be clean relative paths, such as `preferences.md`. Listing returns up
to 100 paths, versions, and deletion flags without content. Pass the returned
`next_offset` as `offset` to continue; `next_offset: 0` means the last page.

Read an entry before changing it, then pass its returned `version` to the write
or delete tool. A path that has never existed returns version 0. Deleted entries
retain their version; use that version to restore them. Tool reads and writes
limit entry content to 1 MiB.

Successful changes persist immediately, even if the task later fails. A version
conflict leaves the current entry unchanged and retains the attempted content in
the conflict history. Read the latest entry before deciding whether to write again.

Each owner can hold at most 20 stores. A store accepts at most 1,000 paths and
1 MiB per entry, enforced by both the tools and the API. Each path retains its
10 most recent revisions and conflicts.

## Schedule runs

1. Open **Deployments** and select an Agent and optional Environment.
2. Set a five-field cron expression and IANA timezone. Leave cron empty for manual runs.
3. Set the run budget and bind Files, Memory, and Vaults. Files require an Environment; Memory does not.
4. Save. Check the next five occurrences and the pinned Agent version.

Edits create a configuration version. Existing runs retain their Agent snapshot. Enable **Use latest Agent version** to resolve the Agent at each trigger. Pausing stops scheduled triggers; manual runs remain available.

Defaults: UTC, skip triggers more than 30 seconds late, and skip a run while the previous task is active. `run_once` launches one run after a missed trigger; it does not replay every missed occurrence. `allow` permits overlapping runs in separate sessions.

Each trigger records `launched`, `skipped`, or `failed`. Missing or archived resources pause the deployment. Fix the configuration, then resume it. Manual retries can use `Idempotency-Key`; reuse the same key only for the same request.

## Delegate to experts

Agent configurations pin expert versions when saved. Updating an expert does not change existing coordinators. In the editor, select **Update experts to current versions** to adopt newer configurations.

`delegation_policy` defaults to `intersection`: experts receive only shared capabilities. `explicit` authorizes each selected expert's own tools and skills. Experts cannot delegate again. All children share the session sandbox and the root budget, with separate message histories.

The coordinator can call:

```json
{"task_ids":["task_example"],"mode":"any","timeout_seconds":60}
```

Use this object as `agent_wait` arguments. Omit `task_ids` for all current children. `mode` is `all` by default. Timeouts range from 0 to 3600 seconds; 0 means no separate wait deadline. The task duration budget still applies. `agent_message` can resume a finished child while its coordinator remains active.

Open **Task → Collaboration**, **Tools**, or **Inputs** to inspect work and intervene. Approve or reject pending calls, submit actual custom-tool results, send input, or cancel a task. Confirm an unknown execution has stopped only after checking its sandbox and tool state.

## Bind credentials

1. Create a **Vault**, then add an exact host and bearer token.
2. Reference the credential ID in an Agent's MCP tool.
3. Bind the Vault to the Session or Deployment.

Tokens are encrypted and never returned by the API. Rotation replaces the token for subsequent calls and requires the current credential version. Archiving a Vault or revoking a credential blocks subsequent calls. **Validate binding** checks the target and local encryption; it does not contact the provider or prove the token is valid there.

Existing credentials without a Vault retain their direct credential-ID binding. Vaults currently store static MCP bearer tokens; OAuth refresh and sandbox secret injection are not supported.

## Receive events

Create a **Webhook** with an HTTPS URL, selected events, and a signing secret of 32–1024 bytes. URLs cannot contain credentials, query parameters, or fragments. Delivery blocks private network targets and redirects.

The body contains event, task, and session identifiers and state. Fetch content through the authenticated API. Deliveries use at-least-once semantics: deduplicate using `Wave-Event-ID`.

Verify `Wave-Signature` as `v1=` followed by the hexadecimal HMAC-SHA256 of `Wave-Timestamp + "." + raw_body`, using the signing secret. Reject stale timestamps and compare signatures in constant time.

Any HTTP 2xx response acknowledges delivery. Other responses and connection failures retry up to eight attempts with exponential delays of 2–128 seconds before the last attempt. Inspect **Webhooks → Deliveries** and retry failed deliveries manually. Pausing stops new event capture; pending deliveries pause when claimed and resume with the subscription. Events during a pause are not replayed.

## Check outputs

Add up to 20 acceptance rules to an Agent:

```json
[{"kind":"json","path":"summary.json","required":["total"],"types":{"total":"number"}}]
```

`file_exists` checks a published artifact. `json` checks a JSON object, required top-level fields, and optional field types. Paths are relative to `/mnt/session/outputs`; omit the path to check the final response. JSON is limited to 1 MiB per check.

**Task → Acceptance** reports `passed`, `failed`, or `error` separately from execution state. Checks run after root artifacts are published for succeeded or partial tasks. They do not automatically rerun the Agent. Benchmark reports include acceptance coverage and pass rate; comparison rejects reduced coverage or a lower pass rate when the baseline includes checks.
