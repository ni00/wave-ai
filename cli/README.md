# wavectl

English | [简体中文](README.zh-CN.md)

Remote CLI for all Wave AI API operations, with task waiting, tool approval, resumable events and streaming files. Use `wave` for local service administration.

## Install and connect

From the repository root (Go only):

```bash
make cli-build
export PATH="${XDG_CACHE_HOME:-$HOME/.cache}/wave-ai/bin:$PATH"
wavectl config set local --url http://localhost:8080
wavectl config set local --key-stdin < /secure/path/wave-api-key
wavectl config use local
wavectl doctor
```

Profiles live in the user configuration directory under `wave-ai/config.json`, written atomically with mode `0600`. `WAVE_PROFILE` overrides the active profile; `WAVE_BASE_URL` and `WAVE_API_KEY` override saved values; explicit `--profile`/`--url` take precedence. Use `config list/show/use/remove/unset-key` to manage connections. Inspection never prints saved keys; local removal does not revoke server credentials.

`doctor` checks database health and API authentication; `doctor --offline` only checks connection settings. Neither proves model or worker readiness.

## Submit and follow work

```bash
wavectl agents list --format table
wavectl agents create --name assistant --model YOUR_MODEL
wavectl sessions create --title research
wavectl tasks create --session SESSION --agent AGENT --input-file task.txt \
  --idempotency-key research-001 --wait
wavectl tasks get TASK --pretty
wavectl tasks wait TASK --timeout 10m
```

Reuse existing IDs where available. `--wait` returns `{task, reason, required_actions}`; an action returns exit code `6`. Submit an authorized decision with `tools approve/reject CALL --task TASK`, or an actual tool result with `tools result CALL --task TASK --result-file result.txt`. Approval does not execute a client tool. After a timeout, continue waiting on the saved task ID rather than creating another task.

## Inputs, output and files

```bash
wavectl schema tasks create --request
wavectl tasks create SESSION --body @request.json --dry-run
wavectl tasks get TASK --select /state --raw
wavectl agents list --all --format jsonl
wavectl events watch SESSION --cursor-file .wave/events.json --reconnects 3
wavectl files upload --file report.txt
wavectl files download FILE --output report.txt
```

Use typed flags or full `--body` JSON, never both. Objects/arrays accept JSON or `@file`; text-file flags accept `-` for stdin. Explicit false and empty strings are preserved. `--dry-run` validates and redacts a preview without contacting the service; JSON/text input is limited to 2 MiB.

stdout defaults to JSON; diagnostics go to stderr. Use `--format table` for reading, `--select` (JSON Pointer) with `--raw` for scalar extraction, and `--all --format jsonl` for incremental pagination. Deadlines default to 30 seconds for requests, 5 minutes for waiting and unlimited for events; override with `--timeout`.

| Exit | Meaning |
| --- | --- |
| 0 | Operation or awaited task succeeded |
| 1–4 | Local/network error, invalid input, authentication error, other API error (respectively) |
| 5 | Timeout/interruption; remote work may continue |
| 6 | Task requires action; inspect stdout |
| 7 | Task ended without success; inspect stdout |

Handle nonzero wait outcomes explicitly in scripts using `set -e`. Cursor files belong to one session; deduplicate replayed event IDs. Downloads replace the destination only on success; `--output -` emits bytes. Stdin uploads use `--file - --filename NAME`.

See the [client skill](../skills/wave-client/SKILL.md) for approvals, recovery and artifacts, [administration](../skills/wave-admin/SKILL.md) for deployment, and [tooling](../tools/clients/README.md) for checks and release builds. API contracts: [English](../api/openapi.json), [Chinese](../api/openapi.zh-CN.json).
