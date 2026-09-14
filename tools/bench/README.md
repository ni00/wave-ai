# Wave benchmarks

English | [简体中文](README.zh-CN.md)

Measure task throughput, latency, and sandbox performance on a target host.

| Mode | Measures | Requires |
| --- | --- | --- |
| `wave bench` | Real HTTP API, workers, PostgreSQL, and a local synthetic model | Docker CLI and a local Docker daemon |
| `wave bench sandbox` | Sandbox creation, command execution, stop, and restart | Docker and the selected sandbox backend |
| `wave bench live` | Deployed service and real model | Wave API key, agent, and a case with assertions |

Synthetic and sandbox modes need no existing deployment or model key. Live mode
consumes model quota; see [real model tests](OBSERVABILITY.md#benchmark-a-real-model).

Run Make commands from the repository root with Go 1.27+, or use a built `wave`
binary. Synthetic and sandbox modes require a host with the Docker CLI.

## Run the runtime benchmark

```bash
make bench
make bench BENCH_ARGS='-workers 2,5,10,20 -tasks 500 -clients 32 -warmup 20 -timeout 5m'
make -s bench BENCH_ARGS='-format json' > bench.json
```

The runtime benchmark uses a temporary database and a local synthetic model. It
ignores deployment `WAVE_*` settings and does not require `make setup`.

### Parameters

| Flag | Default | Meaning |
| --- | --- | --- |
| `-workers` | `2,5,10,20` | Server worker counts tested in sequence; does not change deployment configuration |
| `-clients` | `32` | Maximum in-flight tasks, also limited by task count |
| `-tasks` | `100` | Measured tasks per worker count |
| `-warmup` | `10` | Additional warmup tasks per worker count; 0 disables |
| `-model-delay` | `100ms` | Total simulated streaming delay per model response, distributed across chunks |
| `-poll` | `100ms` | Client task status polling interval |
| `-timeout` | `2m` | Separate total deadline for each warmup and measured phase, including queue time |
| `-format` | `human` | `human` table or full `json` report |

The model emits text only and reports synthetic token usage. Use `-model-delay 0`
to measure service overhead, or a longer delay such as `2s` to compare concurrency
while waiting for the model. See `wave bench --help` for response sizing and other options.

### Workload and timing

Each worker count uses a fresh database. Each client waits for its current task to finish before
submitting the next, in a separate session. Timing includes session creation, task submission, and
status polling; it excludes schema initialization and warmup.

### Metrics

| Metric | Definition |
| --- | --- |
| `successful_tasks_per_second` | Successful tasks / measured phase duration |
| `error_rate` | Failed tasks / attempted tasks, including HTTP errors and timeouts |
| `end_to_end` | Session creation start to observed completion, including polling delay |
| `queue` | Server `started_at - created_at` |
| `execution` | Server `finished_at - started_at`, including model delay and persistence |
| `process_peak_sampled_go_heap_mib` | Whole-process Go heap peak sampled every 100 ms, including Wave, clients, the model, and statistics |
| `db_pool_wait_count`, `db_pool_wait_ms` | Cumulative connection acquisition waits during measurement, with a pool limit of 32 |

Latency distributions include successful tasks only and use nearest-rank
percentiles. `count: 0` means no valid samples; `requested - attempted` counts
unattempted tasks. Heap samples exclude PostgreSQL and do not measure process RSS
or host memory peaks.

Reports go to stdout and progress to stderr. A failed, timed-out, or incomplete
phase returns a nonzero exit code, retains available statistics, and stops later
phases.

## Compare results

1. Keep client count, model settings, polling interval, and task count fixed while
   changing worker count. Too few clients also limit throughput.
2. Use at least 500 tasks and repeat runs to reduce variation in tail estimates.
3. Compare throughput, P95/P99, failure rate, and database connection waits
   together. A throughput plateau with rising latency indicates diminishing
   returns from more workers.
4. Save JSON reports with the revision, host specification, and other workloads.
   Monitor CPU, memory, disk, and containers separately.

The runtime benchmark excludes provider limits and network behavior, sandboxes,
browsers, file transfers, S3, the scheduler, SSE subscribers, and long multi-turn
contexts. Use results to compare hosts and revisions; they do not directly
predict user or sandbox capacity.

## Run the sandbox benchmark

Prepare a backend with the [sandbox setup guide](../../deploy/sandbox-resources.md),
then run:

```bash
make bench-sandbox
make bench-sandbox BENCH_ARGS='-backend gvisor -verify -concurrency 1,2'
```

Supports gVisor (default), Podman, and sbx, using the selected backend's settings.
Capacity is independent of deployment limits, so use a dedicated test host.
This mode does not measure host RSS or peak guest memory.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-backend` | `gvisor` | Sandbox backend under test: `gvisor`, `podman` or `sbx` |
| `-verify` | `false` | Verify results instead of only timing the run |
| `-concurrency` | `1,2` | Concurrency levels tested in sequence |
| `-memory-mib` | `512,1024,2048` | Guest memory sizes tested in sequence |
| `-iterations` | `3` | New sandboxes per client for each memory size |
| `-timeout` | `3m` | Deadline per sample, excluding forced cleanup |

See `wave bench sandbox --help` for image selection, custom commands, and other options.

## Cleanup and verification

Test resources are cleaned up on normal exit or Ctrl-C. After abnormal
termination, use the container name from startup logs with `docker rm -fv <name>`
to remove leftover containers.

After changing benchmark code, run `make bench-test` for race-enabled tests and
Docker integration checks.

Use `wave bench compare baseline.json candidate.json` for regression checks.
See [report comparison](OBSERVABILITY.md#compare-reports).
