# Deployment

English | [简体中文](deployment.zh-CN.md)

Configure service settings, install the gVisor sandbox backend, set resource
limits, and initialize the database.

## Configure the service

Compose reads `deploy/.env`; directly started processes read environment
variables. Common settings are listed below; see the [full configuration example](../deploy/.env.example).

| Setting | Default | Purpose |
| --- | --- | --- |
| `WAVE_WORKER_CONCURRENCY` | `10` | Concurrent task workers per process |
| `WAVE_CONTEXT_TOKENS` | `32000` | Context token budget; keep within the model's limit |
| `WAVE_MODEL_TIMEOUT_SEC` | `300` | Model request timeout in seconds |

Adjust concurrency for provider limits and queue latency. Use the
[benchmarks](benchmarks.md) to measure host capacity. Use HTTPS for external
access and keep the database and storage on a private network. Wave applies no
request rate limits of its own; place a rate-limiting reverse proxy in front of
public deployments.

Back up `deploy/.env`, the database, file storage, and sandbox data. Keep
`WAVE_MASTER_KEY` and storage credentials; replacing the master key makes existing
encrypted credentials unreadable.

## Set up the sandbox backend

| Backend | Requirements | Isolation |
| --- | --- | --- |
| `gvisor` (default) | Docker and `runsc` with `systrap`; no KVM | Userspace kernel |

Wave requires gVisor to run sandboxed tasks.

On an Ubuntu host with Docker installed, run:

```bash
sudo bash deploy/install-sandbox-host.sh
```

The script installs runsc, registers `wave-runsc`, and preserves Docker settings before reloading the daemon.
The runtime requires these flags for file uploads and retained filesystem writes:

```json
{"path":"/usr/bin/runsc","runtimeArgs":["--platform=systrap","--overlay2=none","--file-access=shared"]}
```

See the [gVisor filesystem guide](https://gvisor.dev/docs/user_guide/filesystem/).

Set the backend in `deploy/.env`:

```dotenv
WAVE_SANDBOX_BACKEND=gvisor
WAVE_DOCKER_HOST=unix:///var/run/docker.sock
WAVE_GVISOR_RUNTIME=wave-runsc
WAVE_CONTAINER_IMAGE=docker.io/library/python:3.12-slim-bookworm
WAVE_CONTAINER_NETWORK=none
```

Environments can set `sandbox_backend` to `gvisor` or omit it:

```json
{"name":"python","sandbox_backend":"gvisor","sandbox_profile":"default"}
```

The first sandbox reservation pins the session's backend, engine endpoint, image,
CPU, and memory. Configuration changes apply to new sessions.

## Set resource limits

| Setting | Default | Meaning |
| --- | --- | --- |
| `WAVE_SANDBOX_CPUS` | `1` | Default guest vCPUs |
| `WAVE_SANDBOX_MEMORY_MIB` | `1024` | Default guest memory allocation |
| `WAVE_SANDBOX_MAX_RUNNING` | `2` | Maximum active or reserved session sandboxes |
| `WAVE_SANDBOX_MEMORY_BUDGET_MIB` | `4096` | Total active or reserved guest memory |

Both capacity limits apply across replicas that share a database. These replicas
must use the same engine endpoints and limits.

By default `/workspace` lives in the container writable layer, which has no disk
quota; monitor retained disk usage yourself. Set `WAVE_SANDBOX_WORKSPACE_GIB`
(for example `8`) to place `/workspace` on a size-capped named volume instead.
Volume creation fails loudly on Docker hosts whose volume storage does not
support the `size` option (ext4/XFS is required); sessions created earlier keep
their original layout. Other databases, manually started
sandboxes, and benchmarks are outside this budget. Reserve host memory for Wave,
the database, storage, and sandbox runtime; guest allocations do not measure RSS.

Choose `sandbox_profile` when creating an environment:

| Profile | Allocation | Workload |
| --- | --- | --- |
| Omitted or `default` | Configured CPU and memory defaults | Scripts and file processing |
| `standard` | 2 vCPU / 2048 MiB | Dependency installation, builds, tests |
| `large` | 2 vCPU / 4096 MiB | Browsers and larger builds |

Measure your workload before reducing allocations. gVisor enforces these limits. Changing a profile does not resize an existing sandbox;
create a new session to use the new size.

Per-session sandbox operations (execution, file staging) serialize through a
process-local lock. A session has one active root task at a time, which the
database enforces across replicas; concurrent file staging and task execution
on one session from different replicas is bounded only by Docker's own
call-level concurrency, not by Wave's session lock.

A session with an environment retains one sandbox, shared by its tasks and
subagents. Normal root-task finalization stops compute and retains disk. Pending
approval, unknown outcomes, or failed finalization can retain compute and capacity.
Reservations do not expire with worker leases.

At capacity, new tasks stay queued without consuming their execution timeout.
Cancellation and child tasks that reuse an existing sandbox can proceed. A profile
that exceeds the total memory budget fails admission. Lowering the budget does
not evict running sandboxes. Worker count and sandbox capacity are separate limits.

## Choose an image

Container backends use `python:3.12-slim-bookworm`, with Bash, Python, and pip.
To include Node.js, npm, and Git:

```bash
docker build -f deploy/sandbox.Dockerfile -t wave-ai-sandbox:local .
```

Set `WAVE_CONTAINER_IMAGE=wave-ai-sandbox:local`. Pin image digests for reproducible
sessions; retaining a `latest` reference does not freeze its contents.

Preinstall tools and dependencies for offline execution. Alpine may require
Bash, apt, or glibc compatibility changes; distroless lacks the required shell.

## Files and networking

Container sandboxes receive no engine sockets or Wave credentials. Uploads and
skills use read-only volumes; a stopped staging container handles SDK writes.

Container commands have bounded output, CPU, memory, and PID limits, and
no-new-privileges. Outside the sandbox, the Wave engine itself runs as root
with access to the Docker socket (it must create gVisor containers), and
sandbox containers receive six supplemental capabilities (`CHOWN`,
`DAC_OVERRIDE`, `FOWNER`, `FSETID`, `SETUID`, `SETGID`) on top of `CapDrop:
ALL` to keep file staging working. Treat the engine host as privileged and
keep the Docker socket private to Wave. A timeout stops the session container,
including detached processes. Completed request IDs reuse results; ambiguous
executions are not replayed.

Networking defaults to `none`. For online dependencies, configure a dedicated
engine network and its egress policy. Selecting a network does not filter
destinations. Host and shared-container networking are rejected. Monitor retained
disk usage separately; the memory budget does not cap it.

Wave uses the Docker Go SDK. Container adapters do not invoke engine CLIs. Host setup and disposable
benchmark databases use administrative CLIs.

## Initialize the database

`wave init-db` creates the current schema in an empty database. Repeating it
preserves data; it does not upgrade existing tables. No migration scripts or
historical backfills are maintained during development.

When a development schema changes, stop Wave, clear its dedicated test database
and associated sandbox/file data, then run `wave init-db` and `wave bootstrap`.
Never reset data as part of normal service startup.

## Verify a sandbox

Run on the host with Docker and the selected backend available:

```bash
wave bench sandbox -backend gvisor -verify -concurrency 1,2 \
  -memory-mib 512,1024 -iterations 3 -format json > gvisor.json
```

The benchmark uses disposable PostgreSQL and cleans up its own sandboxes. It
makes no model calls and has an independent capacity budget. The container
`-verify` suite checks execution, isolation, artifacts, deduplication, timeout
cleanup, and disk retention. Cleanup failures identify remaining resources in
the JSON report.

Use `-command` for your workload. Default file/checksum timings and cached-image
runs do not predict agent throughput or peak memory. See [benchmarks](benchmarks.md).
