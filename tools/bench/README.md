# Wave bench

English | [简体中文](README.zh-CN.md)

`wave bench` runs an isolated end-to-end baseline on the target host. It starts disposable PostgreSQL 16, the real Wave HTTP API and workers, and a deterministic streaming model in place of an external provider. No existing deployment, model account, storage account or sandbox is required.

## Running

```bash
make bench
make bench BENCH_ARGS='-workers 2,5,10,20 -tasks 500 -clients 32 -warmup 20 -timeout 5m'
make -s bench BENCH_ARGS='-workers 2,5,10,20 -format json' > bench.json
make bench-test
```

A built `wave` binary can run `wave bench` directly on the host. A local Docker daemon must be available; the first run may pull `postgres:16-alpine`. Compose, `make setup` and a running service are unnecessary. The service image has no Docker CLI; run this tool on the host.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-workers` | `2,5,10,20` | Server worker counts tested in sequence; does not change deployment configuration |
| `-clients` | `32` | Maximum in-flight tasks, also limited by task count |
| `-tasks` | `100` | Measured tasks per worker count |
| `-warmup` | `10` | Additional warmup tasks per worker count; 0 disables |
| `-model-delay` | `100ms` | Total simulated streaming delay per model response, distributed across chunks |
| `-chunks` | `8` | Chunks per synthetic response |
| `-chunk-bytes` | `128` | ASCII bytes per chunk |
| `-poll` | `100ms` | Client task status polling interval |
| `-timeout` | `2m` | Separate total deadline for each warmup and measured phase, including queue time |
| `-format` | `human` | `human` table or full `json` report |

Responses are capped at 4096 chunks and 1 MiB total. The model emits only text and never invokes tools. A zero model delay emphasizes scheduling, HTTP and database overhead; longer delays such as 2s explore worker scaling with model I/O. Token usage is synthetic, with no real tokenization.

## Workload and metrics

Each worker count gets a fresh database, followed by warmup and a timed phase. Clients submit a new task only after their previous task completes: this is closed-loop concurrency. Each task gets a new Session to avoid session serialization. Timing includes session creation, task submission and status polling, but excludes schema initialization and warmup.

- `successful_tasks_per_second`: successful tasks divided by the entire measured phase duration. Failures do not contribute throughput.
- `error_rate`: failed / attempted tasks, including client HTTP errors and timeouts. Requested minus attempted tasks were never attempted.
- `end_to_end`: session creation start to observed completion, including polling delay.
- `queue`: server `started_at - created_at`.
- `execution`: server `finished_at - started_at`, including synthetic model delay and persistence.
- Latency distributions contain successful tasks only and use nearest-rank percentiles. A JSON distribution with `count: 0` has no valid samples. P99 from 100 tasks is coarse; use at least 500 and repeat comparisons.
- `process_peak_sampled_go_heap_mib`: whole-process heap sampled every 100ms, including Wave, the load generator, mock and statistics. This is not RSS, excludes PostgreSQL and is not a host memory peak.
- `db_pool_wait_count` / `db_pool_wait_ms`: cumulative connection acquisition waits during measurement, using the existing service pool limit of 32 connections.

stdout contains only the report; progress goes to stderr. Failures, incomplete runs and timeouts return a nonzero exit code while preserving available measured results. Testing stops after the first failed phase. Zero latency with no successful samples is not a good result.

## Interpreting results

Keep clients, model settings, polling interval and task count fixed while comparing worker counts. Too few clients cap throughput. A throughput plateau with rising latency or connection waits indicates diminishing returns from adding workers. Monitor host CPU, memory, disk and container usage separately; VPS results can vary with other tenants. Keep JSON reports and record the revision, machine specifications and other workloads when comparing hosts or commits.

The baseline excludes real provider limits/network, sandboxes/browsers, uploads/downloads, SeaweedFS/S3, scheduler, SSE subscribers and long multi-turn contexts. The mock uses SSE, but benchmark clients poll task status. Results are not a guarantee of real user or sandbox capacity.

Deployment `WAVE_*` variables are ignored. PostgreSQL binds only a random `127.0.0.1` port. Each run has a unique container name; exit or Ctrl-C removes the container, anonymous volumes and temporary directory. SIGKILL, power loss or an unresponsive Docker daemon can prevent cleanup; use the container name from startup logs with `docker rm -fv <name>` if needed.
