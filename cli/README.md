# wavectl

English | [简体中文](README.zh-CN.md)

Use `wavectl` to connect to Wave AI and manage agents and tasks. Use
[`wave`](#service-administration) to start and administer the service.

## Install and connect

[Start Wave and obtain an API key](../README.md#quick-start). Building the CLI
requires Go 1.23+ and Make. From the repository root:

```bash
make cli-build
export PATH="${XDG_CACHE_HOME:-$HOME/.cache}/wave-ai/bin:$PATH"
wavectl config set local --url http://localhost:8080
wavectl config set local --key-stdin < /secure/path/wave-api-key
wavectl config use local
wavectl doctor
```

Replace `/secure/path/wave-api-key` with the file containing your Wave API key.

`doctor` checks database connectivity and API authentication. `doctor --offline`
checks connection settings only. Submit a task to verify the model and workers.

### Connection profiles

Each profile stores a connection configuration in `wave-ai/config.json` under
your user configuration directory.

| Setting | Precedence, highest first |
| --- | --- |
| Profile | `--profile` → `WAVE_PROFILE` → saved active profile → `default` |
| URL | `--url` → `WAVE_BASE_URL` → profile URL |
| API key | `WAVE_API_KEY` → profile key |

Manage profiles with `config list`, `show`, `use`, `remove`, and `unset-key`.
Removing a key locally does not revoke it on the server.

## Submit and follow a task

```bash
wavectl agents create --name assistant --model YOUR_MODEL
wavectl sessions create --title research
wavectl tasks create --session SESSION --agent AGENT --input-file task.txt \
  --idempotency-key research-001 --wait
```

Set `YOUR_MODEL` to a supported model and `AGENT`/`SESSION` to existing or newly
created IDs. Save the task input in `task.txt` before submitting.

`--wait` prints `{task, reason, required_actions}`. Check the exit code and
`task.state` for the outcome.
After a timeout, keep waiting on the existing task:

```bash
wavectl tasks get TASK --pretty
wavectl tasks wait TASK --timeout 10m
```

Reuse an idempotency key when retrying the same submission; use a new key for a
new task. Reusing a session preserves conversation context and pinned files.

### Resolve required actions

Exit code 6 means the task requires action. Read `type`, `task_id`, and `call_id`
in `required_actions` to choose the next command:

| Action | Response |
| --- | --- |
| `approve_tool` | Use `tools approve` after authorizing execution, or `tools reject` to deny it |
| `submit_tool_result` | Execute the client tool, then submit its actual result with `tools result` |
| `confirm_tool_outcome`, `reconcile_task` | Verify external execution and follow the [recovery workflow](../skills/wave-client/references/recovery.md) |

To allow execution:

```bash
wavectl tools approve CALL --task TASK
wavectl tasks wait TASK
```

For a `submit_tool_result` action, execute the client tool and submit its actual
result:

```bash
wavectl tools result CALL --task TASK --result-file result.txt
```

Use `--is-error` for failures, with an optional `--error-code`. Repeated decisions
have no additional effect; conflicts return HTTP 409. Wait again after resolving
each action.

## Find commands and parameters

```bash
wavectl schema                          # Contract, operations, and version.
wavectl schema tasks create --request   # Request fields.
wavectl schema agents create --example  # Request body template.
wavectl tasks create --help             # Command flags.
```

`completion bash|zsh|fish|powershell` prints shell completion scripts.

Required flags are marked `(required)`. Supply path values as flags or positional
arguments, but not both.

## Input, output, and files

```bash
wavectl tasks create SESSION --body @request.json --dry-run
wavectl tasks get TASK --select /state --raw
wavectl agents list --all --format jsonl
wavectl events watch SESSION --cursor-file .wave/events.json --reconnects 3
wavectl files upload --file report.txt
wavectl files download FILE --output report.txt
```

`--body` accepts JSON, `@file`, or `-`, and cannot be combined with field flags.
Object and array fields accept `@file`. File flags accept `-` for stdin; uploads
also require `--filename NAME`. `--dry-run` validates and previews locally.
`--select` uses a JSON Pointer.

JSON and text input is limited to 2 MiB; `false` and empty strings are valid.
Results go to stdout as JSON, diagnostics to stderr. Use `--format table` for
table output.

Default timeouts are 30 seconds for requests, 5 minutes for task waits, and no
limit for event streams. Override them with `--timeout`. A cursor file belongs
to one session; deduplicate event IDs after reconnecting. Downloads replace the
destination only on success. `--output -` writes raw bytes.

## Exit codes

| Exit | Meaning |
| --- | --- |
| 0 | The operation, or the awaited task, succeeded |
| 1 | Local or network error |
| 2 | Invalid input or usage |
| 3 | Authentication or authorization error |
| 4 | Any other API error |
| 5 | Timeout or interruption; remote work may still be running |
| 6 | Task requires action; inspect stdout |
| 7 | Task ended without success; read stdout |

Errors go to stderr as JSON with `message`, `exit_code`, and, when available,
`hint`, `status`, and `request_id`. Scripts using `set -e` should explicitly handle
nonzero task-wait results, especially codes 6 and 7.

## Service administration

`wave` reads configuration from environment variables.

| Command | Purpose |
| --- | --- |
| `wave serve` | Run the service; `-role all\|api\|worker\|scheduler` overrides `WAVE_ROLE` |
| `wave bootstrap` | Create an organization, user, and API key; requires `-org` |
| `wave init-db` | Create the current schema in an empty database |
| `wave config check` | Validate configuration and print a summary without secrets |
| `wave bench`, `wave bench sandbox` | Capacity baselines; see the [benchmark guide](../tools/bench/README.md) |

`wave bootstrap -format key` prints only the raw key, which can be piped into
`wavectl config set NAME --key-stdin`. See [wave-admin](../skills/wave-admin/SKILL.md)
for deployment, backup, and diagnostics.

## Related documentation

[Task and tool workflows](../skills/wave-client/SKILL.md) · [Client tooling](../tools/clients/README.md) · [English API contract](../api/openapi.json) · [Chinese API contract](../api/openapi.zh-CN.json)
