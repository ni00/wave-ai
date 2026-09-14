# Sandbox resources / 沙箱资源

Wave uses a retained sandbox per **session**, only when the session has an
environment. Workers and subagents in that session share its sandbox. A root
task's normal finalization stops compute and retains disk. A waiting approval,
unknown tool outcome, or failed finalization may retain compute; these count
against capacity until stopped. Reservations have no time-based expiry: an
expired worker lease does not prove its sandbox stopped.

Wave 按会话复用沙箱，未绑定环境的任务不需要沙箱。同一会话的子 agent 共享规格与工作区。
正常根任务收尾停止计算、保留磁盘；等待审批、未知执行结果、失败的收尾可能继续占用额度。
因此 worker 数量不能直接当成可运行沙箱数。

## Defaults and profiles / 默认值与分档

| Setting | Default | Meaning |
| --- | --- | --- |
| `WAVE_SANDBOX_CPUS` | `1` | Default profile guest vCPUs; not dedicated host cores |
| `WAVE_SANDBOX_MEMORY_MIB` | `1024` | Default profile guest memory allocation, not measured host RSS |
| `WAVE_SANDBOX_MAX_RUNNING` | `2` | Maximum active/reserved session sandboxes |
| `WAVE_SANDBOX_MEMORY_BUDGET_MIB` | `4096` | Sum of active/reserved guest memory allocations |

Both capacity limits must fit. They are enforced transactionally in PostgreSQL,
shared across worker replicas using **the same database**. All such replicas
must target the same sandbox engine endpoints and use the same limits. Other Wave databases,
manually started VMs and the sandbox benchmark are outside this budget.
Keep room for the OS, PostgreSQL, storage, Wave, K3s and microVM overhead;
the memory budget is not a host memory cap or an RSS estimate.

容量满时，任务保留在队列中，worker 可以处理其他任务；尚未启动的任务不消耗执行时间预算。
取消任务和已有会话的子任务不会因为没有新沙箱额度而被堵住。超出总内存预算、永远无法满足的
单个规格会使任务失败并给出原因；缩小总预算不会驱逐已运行的沙箱。

Create an environment with optional `sandbox_profile`:

```json
{"name":"build-and-test","sandbox_profile":"standard","packages":{"apt":["build-essential"]}}
```

| `sandbox_profile` | Allocation | Suggested use |
| --- | --- | --- |
| Omitted / `default` | `WAVE_SANDBOX_CPUS` + `WAVE_SANDBOX_MEMORY_MIB` | Small scripts, file processing |
| `standard` | 2 vCPU / 2048 MiB | Dependency installation, ordinary builds/tests |
| `large` | 2 vCPU / 4096 MiB | Browser/build workloads; measure actual requirements |

All three production backends enforce these allocations. The trusted development `local` backend
does not enforce CPU/memory limits. These sizes are starting points, not tested
capacity guarantees; 512 MiB is an experiment requiring runtime/workload checks.

A session's specification (CPU, memory and image reference) is persisted on its
first reservation and retained across process restarts. Configuration/profile
changes affect new session sandboxes; they do not resize existing disks/VMs.
Use a new session to try a different size. Pin `WAVE_SBX_IMAGE` to a verified
release/digest for reproducible images; persisting a `latest` reference does not
make that tag immutable.

## Backend selection / 后端选择

Default: **Docker + gVisor (`gvisor`)**, using `runsc` with **systrap**. It needs
no KVM and adds a userspace kernel boundary for agent code. `podman` requires a
**rootless** Podman API and cgroup v2; it shares the host kernel and is an explicit
compatibility/performance option. `sbx` retains its official SDK and existing VM
integration; its Linux VM host needs KVM. There is no automatic fallback.

```dotenv
WAVE_SANDBOX_BACKEND=gvisor
WAVE_DOCKER_HOST=unix:///var/run/docker.sock
WAVE_GVISOR_RUNTIME=wave-runsc
WAVE_PODMAN_HOST=unix:///run/wave-podman/podman.sock
WAVE_CONTAINER_IMAGE=docker.io/library/python:3.12-slim-bookworm
WAVE_CONTAINER_NETWORK=none
```

Set `WAVE_SANDBOX_BACKEND` to `gvisor`, `podman` or `sbx`. Alternatively choose
per environment: `{"name":"python","sandbox_backend":"podman","sandbox_profile":"default"}`.
The first reservation pins the backend, engine endpoint, image, CPU and memory.
Changing the default affects new sessions; retained sessions keep their backend.
All backends share the same DB admission budget. Legacy `WAVE_SBX_*` resource
variables remain supported; `WAVE_SANDBOX_*` takes precedence.

Run `sudo bash deploy/install-sandbox-host.sh` on an Ubuntu host with Docker
already installed. It installs runsc from the official gVisor apt repository,
registers `wave-runsc`, and starts a dedicated rootless Podman user/socket.
It preserves Docker daemon settings and reloads Docker without restarting it.
The registered runtime must include **all three** flags:

```json
{"path":"/usr/bin/runsc","runtimeArgs":["--platform=systrap","--overlay2=none","--file-access=shared"]}
```

The last two flags make SDK archive operations and retained root filesystem
changes consistent; gVisor's default internal overlay is unsuitable for this
contract. See the [official filesystem guide](https://gvisor.dev/docs/user_guide/filesystem/).

The base Compose file mounts the Docker socket only into the trusted Wave service.
Add `-f deploy/compose.podman.yaml` to enable Podman selection too. Sandbox
containers receive neither engine socket nor Wave credentials. Engine APIs are
accessed through the official Docker Go SDK; Podman uses its documented Docker
v1.40 compatibility API. No engine CLI is invoked by the container adapters.
Host installation and disposable benchmark PostgreSQL setup use admin CLIs.

Uploads and skills are named volumes mounted **read-only** in agent containers.
A stopped staging container provides SDK access to their writable side; it never
runs and never shares that access with the agent. Commands have bounded output,
CPU/memory/PID limits, no-new-privileges, and restricted capabilities. Timeout
cleanup kills the entire session container, including detached children. A stable
request ID reuses a completed result; an ambiguous execution is never replayed.

Default networking is `none`. Bake dependencies into the image for offline agents.
If online package installs are needed, configure a dedicated engine network and
its egress policy; choosing a network does **not** implement destination filtering.
Host/shared-container networking is rejected. Container disk usage is retained
and is not capped by the memory budget; monitor disk space separately.

## Image choice / 镜像选择

Container backends default to Debian-based `python:3.12-slim-bookworm`, with Bash,
Python and pip. It is smaller in scope than a full Ubuntu agent image and does
not include an inner Docker daemon. For Node/npm/git as well:

```bash
docker build -f deploy/sandbox.Dockerfile -t wave-ai-sandbox:local .
# Set WAVE_CONTAINER_IMAGE=wave-ai-sandbox:local.
# For Podman, build/load the image into the dedicated rootless engine too.
```

Use a trusted, pinned image digest for reproducibility. Preinstall common tools
once; adding Go, Java or browsers increases image size and workload memory needs.
Alpine is not the default because of Bash/apt/glibc compatibility; distroless
cannot support this shell contract. sbx keeps its separate compatible image
`docker.io/docker/sandbox-ubuntu-24.04:latest`; arbitrary OCI images are not
assumed to implement the sbx guest services. Its existing file-mode staging is
not equivalent to the container backends' read-only mounts.

## Database initialization / 数据库初始化

`wave init-db` creates the complete current schema from the application models
in an empty database. Historical migration scripts and schema backfills are not
maintained during development. Repeated initialization preserves existing data.

当前直接使用最终表结构。开发期间结构变化时，停止 Wave，清空其专用测试数据库及旧沙箱、
文件数据，再运行 `wave init-db` 和 `wave bootstrap`。不要在服务启动时自动删除数据。
初始化后，使用 `WAVE_SANDBOX_*` 设置新会话的资源规格。

## Benchmark / 压测

`wave bench` is the synthetic API/worker test. `wave bench sandbox` exercises
real sandboxes with disposable PostgreSQL, then deletes only its own test
containers/volumes or VMs. It never contacts a model provider. Benchmarks have
independent admission budgets: allow headroom alongside running deployments.

```bash
wave bench sandbox -backend gvisor -verify -concurrency 1,2 \
  -memory-mib 512,1024 -iterations 3 -format json > gvisor.json
wave bench sandbox -backend podman -verify -concurrency 1 \
  -memory-mib 1024 -iterations 1 -format json > podman.json
wave bench sandbox -backend sbx -memory-mib 1024 -concurrency 1 -iterations 1
```

The container `-verify` suite checks Bash/Python, credential/socket absence,
read-only uploads/skills, artifact publication, request deduplication, bounded
binary output, timeout child cleanup, and disk retention after stop/start.
The ordinary workload is a small file/checksum test. Use `-command` for actual
Python/Node/build work; these lifecycle timings are not an agent throughput
estimate. Images are cached between samples. The report does not measure host
RSS or peak guest memory; provision headroom for runsc, Docker/Podman and services.
Cleanup failures retain the exact name in the JSON report for reconciliation.
