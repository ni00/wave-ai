# Examples

Runnable end-to-end examples against a running Wave deployment. Each script
creates its own agents, environments, files, and tasks, waits for terminal
states, and prints the results.

## Prerequisites

1. A running deployment (`make setup`, configure `deploy/.env`, then
   `docker compose --project-directory deploy up -d --build`).
2. gVisor installed on the host for sandboxed tools:
   `sudo bash deploy/install-sandbox-host.sh` (Ubuntu; see
   [deployment docs](../docs/deployment.md)).
3. An API key from `wave bootstrap`.
4. `curl` and `jq` on your PATH.

## Run

```bash
export WAVE_API_KEY=wa-xxxxxxxx            # from wave bootstrap
export BASE=http://localhost:8080          # your Wave endpoint
bash examples/01-data-analysis.sh
```

| Script | Demonstrates |
| --- | --- |
| [01-data-analysis.sh](01-data-analysis.sh) | Environment binding, file upload, bash tool, python stdlib analysis, output artifact |
| [02-code-review.sh](02-code-review.sh) | Read-only review agent over an uploaded Go file |
| [03-writing-team.sh](03-writing-team.sh) | Multi-agent delegation: coordinator, writer, editor via `expert_ids` |
| [04-schedule.sh](04-schedule.sh) | Cron deployment: register, trigger manually, verify, pause |

## Notes

- Scripts are idempotent: each run creates fresh resources.
- Model calls consume provider quota; scripts set explicit per-task budgets
  (`max_tokens`, `max_tool_calls`, `max_seconds`).
- The default sandbox has no network access. Use the standard library or bake
  dependencies into a custom image (`WAVE_CONTAINER_IMAGE`).
- See [docs/deployment.md](../docs/deployment.md) for configuration, and the
  [Simplified Chinese guide](README.zh-CN.md) for the Chinese version of this
  page.
