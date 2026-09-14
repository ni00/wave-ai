# Wave AI

English | [简体中文](README.zh-CN.md)

Wave AI is a self-hosted agent service. Use its API to define agents, submit tasks,
and track execution.

## Capabilities

- **Task recovery.** Save progress and resume recoverable work after restarts.
  Uncertain tool outcomes require confirmation.
- **Tool approval.** Pause for approval before key operations, or let clients
  execute tools and submit results.
- **Progress tracking.** Read events over SSE or polling and resume from a cursor.
- **Sandboxed execution.** Isolate shell and file tools. Stop compute when tasks
  finish while retaining files.
- **Files, skills, and memory.** Supply inputs, reusable skills, and cross-task
  memory, and collect output artifacts.
- **Versions and schedules.** Pin each task to an agent version and schedule runs
  with cron.

## Quick start

You need Docker Compose, Make, OpenSSL, and a model endpoint compatible with the
OpenAI Chat Completions API. Run commands from the repository root.

1. Generate the deployment configuration:

   ```bash
   make setup
   ```

2. Edit `deploy/.env`. Set `WAVE_DB_PASSWORD`, `WAVE_MODEL_BASE_URL`, and
   `WAVE_MODEL_API_KEY`. The model endpoint must support streaming
   `POST /chat/completions` requests.

3. Start the service and create a Wave API key:

   ```bash
   make up
   docker compose --project-directory deploy exec wave wave bootstrap -org demo -user admin
   ```

   Save the printed key; it is shown only once. Clients use this **Wave API key**
   to access Wave. `WAVE_MODEL_API_KEY` authenticates Wave to the model provider.

4. Open [Swagger UI](http://localhost:8080/swagger/index.html). In **Authorize**,
   enter the Wave API key without the `Bearer` prefix.

`make down` stops the containers and retains data volumes. Database initialization
creates the current schema in an empty database. Historical schema migrations
are not provided; see [database initialization](deploy/sandbox-resources.md#initialize-the-database)
before changing versions.

### Submit a task

This Bash example requires `curl` and `jq`. Set `WAVE_API_KEY` to the key from
`bootstrap` and replace `YOUR_MODEL` with a model supported by your provider.

```bash
BASE=http://localhost:8080
auth=(-H "Authorization: Bearer ${WAVE_API_KEY:?Set WAVE_API_KEY first}" \
  -H 'Content-Type: application/json')

agent=$(curl -fsS "$BASE/v1/agents" "${auth[@]}" \
  -d '{"name":"assistant","model":"YOUR_MODEL"}' | jq -er .id)

session=$(curl -fsS "$BASE/v1/sessions" "${auth[@]}" \
  -d '{"title":"first run"}' | jq -er .id)

task=$(curl -fsS "$BASE/v1/sessions/$session/tasks" "${auth[@]}" \
  -H "Idempotency-Key: first-run-$session" \
  -d "$(jq -n --arg agent "$agent" '{agent_id: $agent, input: "Say hello"}')" | jq -er .id)

curl -fsS "$BASE/v1/tasks/$task" "${auth[@]}" | jq '{id,state,result}'
```

Task creation returns HTTP 202; execution continues asynchronously. Repeat the
last request to check progress, or use the [CLI](cli/README.md) or an SDK to wait
for completion. Reuse the task ID after a client timeout. For retries of the same
submission, reuse its idempotency key.

## Execution model

| Concept | Purpose |
| --- | --- |
| Agent | Versioned model, instructions, tools, and skills. |
| Session | Conversation and workspace context; one active root task at a time. |
| Task | One execution using a fixed agent version, bounded by token, tool-call, time, and other budgets. |
| Environment | Sandbox backend, resource profile, and packages. |
| Deployment | An agent's execution schedule and run history. |

A `waiting` task may need approval or a tool result. An `unknown` task requires
verification of its execution. Inspect required actions through the CLI or an SDK,
then follow the [tool workflow](skills/wave-client/references/tools.md) or
[recovery workflow](skills/wave-client/references/recovery.md).

## Deployment

### Model and worker configuration

Compose reads `deploy/.env`; directly started processes read environment
variables. Common settings are listed below; see the [full configuration example](deploy/.env.example).

| Setting | Default | Purpose |
| --- | --- | --- |
| `WAVE_WORKER_CONCURRENCY` | `10` | Concurrent task workers per process |
| `WAVE_CONTEXT_TOKENS` | `32000` | Context token budget; keep within the model's limit |
| `WAVE_MODEL_TIMEOUT_SEC` | `300` | Model request timeout in seconds |

Adjust concurrency for provider limits and queue latency. Use the
[benchmarks](tools/bench/README.md) to measure host capacity. Use HTTPS for external
access and keep the database and storage on a private network.

### Sandboxes and backups

Docker with gVisor is the default; rootless Podman and sbx are also supported.
Follow the [sandbox setup guide](deploy/sandbox-resources.md) before running
sandboxed tasks. The `local` backend uses the service user's host permissions and
is intended for trusted development.

Each sandbox defaults to 1 vCPU and 1 GiB of memory. Replicas share default limits
of 2 active or reserved sandboxes and 4 GiB of guest memory. Sandbox quotas and
task concurrency are configured separately.

Back up `deploy/.env`, the database, file storage, and sandbox data. Keep
`WAVE_MASTER_KEY` and storage credentials; replacing the master key makes existing
encrypted credentials unreadable.

For development without Compose, see the [local setup instructions](skills/wave-admin/references/setup.md).

## Clients and documentation

| Use case | Guide |
| --- | --- |
| Manage resources and follow tasks | [wavectl](cli/README.md) |
| Integrate an application | [Go SDK](sdks/go/README.md), [Python SDK](sdks/python/README.md), [TypeScript SDK](sdks/typescript/README.md) |
| Give an external agent Wave workflows | [wave-client skill](skills/wave-client/SKILL.md) |
| Operate a deployment | [wave-admin skill](skills/wave-admin/SKILL.md) |
| Inspect API requests and responses | [English OpenAPI](api/openapi.json), [Chinese OpenAPI](api/openapi.zh-CN.json) |
| Measure runtime, model, and sandbox performance | [Benchmarks](tools/bench/README.md) |
| Query logs, traces, and metrics | [Observability](tools/bench/OBSERVABILITY.md) |
| Use the web console | [Console guide](web/README.md) |

Swagger UI supports English and Chinese. Use the language menu or open
[the Chinese view](http://localhost:8080/swagger/index.html?lang=zh-CN).

## Development

```bash
make build                       # Build the service.
make test                        # Run unit and architecture tests.
make check                       # Check contracts, vet, verify modules, and build.
make docs clients-generate       # Regenerate API documentation and clients.
make clients-check clients-test  # Check generated clients and behavior.
```

See [client tooling](tools/clients/README.md) for dependencies, integration tests,
and packaging.

[Configure schedules, delegation, Vaults, Webhooks, and acceptance checks](deploy/managed-agents.md).
