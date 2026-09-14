# Sandbox setup

English | [简体中文](sandbox-resources.zh-CN.md)

## Choose a backend

| Backend | Requirements | Isolation |
| --- | --- | --- |
| `gvisor` (default) | Docker and `runsc` with `systrap`; no KVM | Userspace kernel |
| `podman` | Rootless Podman API and cgroup v2 | Shares the host kernel |
| `sbx` | sbx VM host; KVM on Linux | Virtual machine |

Wave does not fall back to another backend. The `local` backend runs with the
service user's host permissions and does not enforce resource limits. Use it
only for trusted development.

On an Ubuntu host with Docker installed, run:

```bash
sudo bash deploy/install-sandbox-host.sh
```

The script installs runsc, registers `wave-runsc`, and starts a dedicated rootless
Podman user and socket. It preserves Docker settings and reloads the daemon.
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
WAVE_PODMAN_HOST=unix:///run/wave-podman/podman.sock
WAVE_CONTAINER_IMAGE=docker.io/library/python:3.12-slim-bookworm
WAVE_CONTAINER_NETWORK=none
```

To enable Podman, include `-f deploy/compose.podman.yaml` in Compose commands.
To select a backend for one environment, set `sandbox_backend`:

```json
{"name":"python","sandbox_backend":"podman","sandbox_profile":"default"}
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
must use the same engine endpoints and limits. Other databases, manually started
sandboxes, and benchmarks are outside this budget. Reserve host memory for Wave,
the database, storage, and sandbox runtime; guest allocations do not measure RSS.
Legacy `WAVE_SBX_*` resource variables are supported; `WAVE_SANDBOX_*` takes precedence.

Choose `sandbox_profile` when creating an environment:

| Profile | Allocation | Workload |
| --- | --- | --- |
| Omitted or `default` | Configured CPU and memory defaults | Scripts and file processing |
| `standard` | 2 vCPU / 2048 MiB | Dependency installation, builds, tests |
| `large` | 2 vCPU / 4096 MiB | Browsers and larger builds |

Measure your workload before reducing allocations. All three production backends
enforce these limits. Changing a profile does not resize an existing sandbox;
create a new session to use the new size.

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

Set `WAVE_CONTAINER_IMAGE=wave-ai-sandbox:local`. For Podman, also build or load
that image into the dedicated rootless engine. Pin image digests for reproducible
sessions; retaining a `latest` reference does not freeze its contents.

Preinstall tools and dependencies for offline execution. Alpine may require
Bash, apt, or glibc compatibility changes; distroless lacks the required shell.
sbx uses its own guest image, `docker.io/docker/sandbox-ubuntu-24.04:latest`;
arbitrary OCI images may lack its guest services. Set `WAVE_SBX_IMAGE` to a
verified release or digest.

## Files and networking

Container sandboxes receive no engine sockets or Wave credentials. Uploads and
skills use read-only volumes; a stopped staging container handles SDK writes.
sbx uses file-mode staging, which does not provide equivalent read-only mounts.

Container commands have bounded output, CPU, memory, and PID limits, restricted
capabilities, and no-new-privileges. A timeout stops the session container,
including detached processes. Completed request IDs reuse results; ambiguous
executions are not replayed.

Networking defaults to `none`. For online dependencies, configure a dedicated
engine network and its egress policy. Selecting a network does not filter
destinations. Host and shared-container networking are rejected. Monitor retained
disk usage separately; the memory budget does not cap it.

Wave uses the Docker Go SDK for Docker and Podman's v1.40 compatibility API for
Podman. Container adapters do not invoke engine CLIs. Host setup and disposable
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
wave bench sandbox -backend podman -verify -concurrency 1 \
  -memory-mib 1024 -iterations 1 -format json > podman.json
wave bench sandbox -backend sbx -memory-mib 1024 -concurrency 1 -iterations 1
```

The benchmark uses disposable PostgreSQL and cleans up its own sandboxes. It
makes no model calls and has an independent capacity budget. The container
`-verify` suite checks execution, isolation, artifacts, deduplication, timeout
cleanup, and disk retention. Cleanup failures identify remaining resources in
the JSON report.

Use `-command` for your workload. Default file/checksum timings and cached-image
runs do not predict agent throughput or peak memory. See [Benchmarks](../tools/bench/README.md).
