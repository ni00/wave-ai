# Wave AI console

English | [简体中文](README.zh-CN.md)

## Open the console

Start Wave, then open [the console](http://localhost:8080/console/). Sign in with a
Wave API key issued by `wave bootstrap`. The key stays in page memory; reloads
require another sign-in. Resources are scoped to their owner, including within
an organization.

Use HTTPS or an SSH tunnel for remote access. Set `VPS_USER` and `VPS_HOST` locally:

```sh
ssh -N -L 18080:127.0.0.1:8080 "${VPS_USER}@${VPS_HOST}"
```

Open [the tunneled console](http://localhost:18080/console/).

## Browse resources

| Section | Pages |
| --- | --- |
| Monitoring | Overview, Metrics, Traces, Logs |
| Resources and execution | Environments, Files, Memory, Sessions, Tasks |
| Performance testing | Benchmarks |

Search and filter lists, then select a record for details. Press `/` to search,
↑/↓ to select list rows, and Escape to close details. Copy the URL to share the
selected resource. Lists use cursor pagination and load details on demand.
Optional live refresh runs every five seconds while the page is visible.

- **Metrics:** select a range from one hour to 30 days, or a custom range. Select
  a chart point to open that interval's traces, failed tasks, or logs. Arrow keys
  move between points.
- **Traces:** inspect the call tree, timeline, and span details. Follow task,
  child-task, and log links.
- **Logs:** filter by time, level, module, message, error, or ID. The Trace link
  opens the associated span. Execution events are in **Tasks/Sessions → Events**.
- **Files:** preview up to 128 KiB as text. Browser downloads are limited to
  64 MiB; use `wavectl files download` for larger files.
- **Memory:** inspect entries, revisions, and conflicts.

See [observability](../tools/bench/OBSERVABILITY.md) for metric definitions,
collection limits, and retention.

## Compare benchmarks

In **Benchmarks**, import a v2 JSON report from `wave bench` or `wave bench live`
(up to 16 MiB). Reports persist as files. CLI runs do not upload automatically;
importing the same report twice creates two records.

Select a baseline from the latest 200 reports to compare throughput, success
rate, and P50/P95/P99. Comparisons require matching workloads, load settings,
models, and worker counts. Use `wave bench compare` for regression checks.
See the [benchmark guide](../tools/bench/README.md).

## Develop the frontend

Start a local Wave API, then run from the repository root:

```sh
npm ci --ignore-scripts --prefix web
npm run dev --prefix web
```

The proxy defaults to `http://127.0.0.1:8080`; override it with `WAVE_WEB_API_URL`.

```sh
make web-build
make web-test
```

Commit rebuilt assets under `internal/platform/webui/static/` with frontend
changes. Go builds embed these files; Docker builds regenerate them. Do not commit
`web/dist`. Keep the Sites packaging files for optional Sites builds.

Assets use gzip and content hashes; HTML uses ETag validation. Metrics, Logs,
Traces, and resource details load in separate chunks. For API changes, also run
`make integration`, `make clients-check clients-test`, and `make check`.
