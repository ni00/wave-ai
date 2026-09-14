# Logs, traces, and metrics

English | [简体中文](OBSERVABILITY.zh-CN.md)

## Inspect a task trace

Use a Wave API key, not a model-provider key:

```bash
wave trace -key-file /path/to/wave-api-key -task TASK_ID
wave trace -key-file /path/to/wave-api-key -task TASK_ID -format json > trace.json
wave trace -key-file /path/to/wave-api-key -task TASK_ID -format chrome > timeline.json
wavectl tasks trace --id TASK_ID
```

`GET /v1/tasks/{id}/trace` uses owner authorization. The root task's `root_id`
is the trace ID; child tasks share it but have separate task and span IDs. Query
a child task to inspect its internal calls. Open `timeline.json` in a viewer
that supports Chrome Trace Event format.

| Field | Definition |
| --- | --- |
| `wall_ms` | Creation to completion; null while unfinished |
| `initial_queue_ms` | Creation to first execution |
| `model_ms` | Complete model calls for this task, including retries, compaction, network, and stream handling |
| `first_delta_ms` | First model delta; not necessarily the first visible text token |
| `tool_ms` | Executed tool calls with complete timing; external-result and `agent_wait` waits are separate `tool_wait` spans |
| `phase_ms` | Top-level `workspace.prepare` and `workspace.finish`; do not add nested spans again |
| `unattributed_ms` | Wall time outside the union of known intervals; includes scheduling and persistence, not just CPU work |
| `input_tokens`, `output_tokens` | This task's model usage, excluding child-task duplication; cached tokens are a subset of input |

Missing boundaries remain unknown, not zero. `usage_known=false` means token
counts are partial. A query includes at most 5,000 model calls, 5,000 tool calls,
10,000 phase boundaries, and 5,000 child tasks. Check `truncated` and `incomplete`.
Historical tasks cannot recover phase timings that were never recorded.

## Query logs

Wave writes JSON logs to stderr. Set `WAVE_LOG_LEVEL` to `DEBUG`, `INFO`, `WARN`,
or `ERROR`. HTTP responses include `X-Request-ID`; task creation, task lookup,
and trace lookup also return `X-Wave-Trace-ID`.

```bash
docker compose --project-directory deploy logs --no-log-prefix wave \
  | jq -c 'select(.trace_id == "TASK_ID")'
```

Logs use `request_id`, `trace_id`, `session_id`, `task_id`, and `span_id` for
correlation. **Logs** in the [console](../../web/README.md) supports search and
links to the matching trace span. Task and session execution events are separate.

Only owner-scoped HTTP and worker logs are persisted. Startup and maintenance
logs stay in stderr. Successful console reads also stay in stderr to avoid
logging each monitoring refresh. Stored fields include message, level, module,
correlation IDs, allowed metadata, and safe error codes. Prompts, model output,
tool commands, credentials, and raw exceptions are excluded.

Log persistence is best effort: a 4,096-record queue batches up to 128 records or
250 ms. Failed batches retry; queue overflow drops new records and emits
`monitor.logs_dropped`. A process crash can lose queued records. Compose rotates
stderr logs at 10 MiB × 3 files.

Phase writes have a 250 ms timeout and worker-fence checks. Failures emit
`trace.write_failed` without retrying tools. A missing end produces an incomplete
span; if both boundaries are missing, inspect warnings and unattributed time.
Phase events follow task-event retention; model deltas do not create per-token spans.

## Read metrics

Select a time range in **Metrics**, then select a chart point to find traces,
failed tasks, or logs from that interval. Task filters use completion time.

| Metric | Definition |
| --- | --- |
| Task throughput | Completed tasks per minute; root and child tasks each count |
| Success rate | Succeeded / all completed tasks, including failed, partial, and canceled |
| Task latency | Creation to completion; P50/P95/P99 |
| Model first delta | First model increment; P50/P95 |
| Token usage | Per-call input, output, and cached input; cached input is not added twice |
| Queue wait | Initial queue wait only |
| Sandbox startup | `sandbox.ensure`, including reuse checks |
| Model latency | Model calls, including retries and compaction |

Unknown usage and interrupted-call durations are not filled with zero. Empty
latency and success-rate values display `—`. Percentiles use merged logarithmic
histograms with 2% bins, not averages of bucket percentiles.

Metrics aggregate continuously into minute and hour buckets. Queries read these
buckets, not historical traces or tasks. Labels use fixed metric names and owner
scope, without task, session, or model IDs. Task/model samples commit with state
changes; sandbox samples use best-effort phase capture. Bucket updates and sample
deletion are transactional across replicas.

| Data | Retention |
| --- | --- |
| Structured logs | 7 days |
| Minute buckets | 48 hours |
| Hour buckets | 31 days |

Queries cover at most 30 days and align outward to bucket boundaries. Pending
samples remain until aggregated; the console shows aggregation lag over five
seconds. Collection starts when enabled, with no backfill from old events.
See [database initialization](../../deploy/sandbox-resources.md#initialize-the-database)
when changing schemas.

## Benchmark a real model

Prepare a case using an existing agent. The agent selects the model and tools:

```bash
cp tools/bench/example.live.json /tmp/case.json
# Set agent_id in /tmp/case.json.
wave bench live -case /tmp/case.json -key-file /path/to/wave-api-key \
  -tasks 10 -clients 1 -timeout 5m > baseline.json
wave bench live -case /tmp/case.json -key-file /path/to/wave-api-key \
  -tasks 10 -clients 1 -timeout 5m > candidate.json
wave bench compare -max-p95-regression 10 baseline.json candidate.json
```

Live runs create real sessions and tasks and consume model quota. Set `-url` to
select the service. Provider credentials come from the service configuration.
Each task gets its own session; the case can set `environment_id`, `file_ids`,
and `memory_store_ids` under `session`. Shared memory stores can cause write contention.

A case requires a deterministic assertion: `result_equals`, `result_contains`,
or an artifact check:

```json
{"expect":{"files":[{"path":"summary.json","json_equals":{"answer":42}}]}}
```

Use `sha256` for exact bytes. `json_equals` compares structure and values,
ignoring object-key order. Each checked artifact is limited to 2 MiB. Failed
assertions count as `invalid_result`. Reports store workload hashes, agent
versions, model names, timing, and trace metadata, not task inputs or outputs.

On timeout or required action, the runner requests cancellation and records
`cancel_requested` or `cancel_error`. Cancellation does not prove external work
stopped; reconcile `unknown` outcomes using the [recovery workflow](../../skills/wave-client/references/recovery.md).
Sessions and artifacts remain available for inspection.

## Compare reports

Synthetic and live benchmarks produce v2 reports with task traces. Sandbox
reports use a separate format. Do not compare different modes.

Reports include revision, success rate, throughput, P50/P95/P99, model/tool timing,
rounds, and token usage. Live Go/CPU metadata describes the load generator;
server worker count is unknown and recorded as 0.

End-to-end latency covers session creation, submission, and polling until
observed completion. It excludes assertions and trace downloads; throughput
includes those costs. `-poll` affects latency and API load. `-warmup` excludes
samples from measured distributions but still creates tasks and uses model quota.

Compare matching case hashes, load settings, and worker stages. Runs with
failures, unfinished tasks, or missing traces cannot pass. A P95 regression above
`-max-p95-regression` returns a nonzero exit code. Latency percentiles include
only validated successes; inspect failures, unattempted tasks, and sample counts
as well. Fewer than 20 successes produce exploratory tail estimates. Repeat runs
before attributing changes to an optimization.

See [Benchmarks](README.md) for synthetic and sandbox tests.
