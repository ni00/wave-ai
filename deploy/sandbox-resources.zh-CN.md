# 沙箱部署

[English](sandbox-resources.md) | 简体中文

## 选择后端

| 后端 | 依赖 | 隔离方式 |
| --- | --- | --- |
| `gvisor`（默认） | Docker、使用 `systrap` 的 `runsc`；无需 KVM | 用户态内核 |
| `podman` | Rootless Podman API、cgroup v2 | 共享宿主机内核 |
| `sbx` | sbx VM 宿主机；Linux 需要 KVM | 虚拟机 |

后端不可用时不会自动切换。`local` 后端以服务用户的宿主机权限执行，不限制资源，仅用于可信开发环境。

在已安装 Docker 的 Ubuntu 主机上运行：

```bash
sudo bash deploy/install-sandbox-host.sh
```

脚本安装 runsc、注册 `wave-runsc`，并启动专用 rootless Podman 用户和 socket。脚本保留 Docker 配置并重载 daemon。为保证文件上传和文件系统写入持久化，runtime 必须包含以下参数：

```json
{"path":"/usr/bin/runsc","runtimeArgs":["--platform=systrap","--overlay2=none","--file-access=shared"]}
```

参见 [gVisor 文件系统说明](https://gvisor.dev/docs/user_guide/filesystem/)。

在 `deploy/.env` 中设置后端：

```dotenv
WAVE_SANDBOX_BACKEND=gvisor
WAVE_DOCKER_HOST=unix:///var/run/docker.sock
WAVE_GVISOR_RUNTIME=wave-runsc
WAVE_PODMAN_HOST=unix:///run/wave-podman/podman.sock
WAVE_CONTAINER_IMAGE=docker.io/library/python:3.12-slim-bookworm
WAVE_CONTAINER_NETWORK=none
```

启用 Podman 时，在 Compose 命令中添加 `-f deploy/compose.podman.yaml`。也可为单个环境设置 `sandbox_backend`：

```json
{"name":"python","sandbox_backend":"podman","sandbox_profile":"default"}
```

首次预留沙箱时，会话固定后端、引擎地址、镜像、CPU 和内存。后续配置修改只影响新会话。

## 设置资源限制

| 配置项 | 默认值 | 含义 |
| --- | --- | --- |
| `WAVE_SANDBOX_CPUS` | `1` | 默认客体 vCPU 数 |
| `WAVE_SANDBOX_MEMORY_MIB` | `1024` | 默认客体内存分配量 |
| `WAVE_SANDBOX_MAX_RUNNING` | `2` | 活动或预留沙箱数上限 |
| `WAVE_SANDBOX_MEMORY_BUDGET_MIB` | `4096` | 活动或预留沙箱的客体内存总量上限 |

两个容量限制同时生效，由共享数据库的副本共同执行。各副本必须使用相同引擎地址和限制。其他数据库、手动启动的沙箱和压测不在此预算内。为 Wave、数据库、存储和沙箱运行时预留宿主机内存；客体分配量不等于 RSS。
旧的 `WAVE_SBX_*` 资源变量仍受支持，`WAVE_SANDBOX_*` 优先。

创建环境时选择 `sandbox_profile`：

| 规格 | 分配量 | 工作负载 |
| --- | --- | --- |
| 省略或 `default` | 配置中的默认 CPU 和内存 | 脚本、文件处理 |
| `standard` | 2 vCPU / 2048 MiB | 安装依赖、构建、测试 |
| `large` | 2 vCPU / 4096 MiB | 浏览器、较大构建 |

缩减规格前先测试实际负载。三个生产后端均执行资源限制。修改规格不会调整已有沙箱，需新建会话。

绑定环境的会话保留一个沙箱，由同一会话的任务和子 Agent 共享。根任务正常收尾时停止计算、保留磁盘。等待审批、未知执行结果或收尾失败时，可能继续占用计算资源和额度。Worker 租约到期不会释放沙箱预留。

容量满时，新任务留在队列中，不消耗执行超时。取消任务和复用已有沙箱的子任务仍可继续。单个规格超过总内存预算时准入失败；降低预算不会驱逐运行中的沙箱。Worker 数与沙箱容量分别限制。

## 选择镜像

容器后端默认使用 `python:3.12-slim-bookworm`，包含 Bash、Python 和 pip。需要 Node.js、npm 和 Git 时构建：

```bash
docker build -f deploy/sandbox.Dockerfile -t wave-ai-sandbox:local .
```

设置 `WAVE_CONTAINER_IMAGE=wave-ai-sandbox:local`。使用 Podman 时，也需将镜像构建或加载到专用 rootless 引擎。固定镜像 digest 可保证会话可复现；保存 `latest` 引用不会固定镜像内容。

离线执行需预装工具和依赖。Alpine 可能需要适配 Bash、apt 或 glibc；distroless 缺少所需 shell。
sbx 使用独立的客体镜像 `docker.io/docker/sandbox-ubuntu-24.04:latest`，任意 OCI 镜像不一定包含所需客体服务。可用 `WAVE_SBX_IMAGE` 指定已验证的版本或 digest。

## 文件与网络

容器沙箱不接收引擎 socket 或 Wave 凭据。上传文件和技能使用只读卷，SDK 通过停止状态的 staging 容器写入。sbx 的文件权限 staging 不等同于只读挂载。

容器命令限制输出、CPU、内存、PID 和 capabilities，并启用 no-new-privileges。超时会停止整个会话容器，包括后台进程。已完成的请求 ID 复用结果，结果不明的执行不会重放。

网络默认为 `none`。需要在线安装依赖时，配置专用引擎网络及出口策略；选择网络本身不会过滤目标地址。不允许 host 或共享容器网络。保留磁盘的占用需单独监控，不受内存预算限制。

Wave 通过 Docker Go SDK 访问 Docker，通过 Podman v1.40 兼容 API 访问 Podman。容器适配器不调用引擎 CLI；宿主机安装和临时压测数据库使用管理 CLI。

## 初始化数据库

`wave init-db` 在空数据库中创建当前表结构。重复运行保留数据，不升级已有表。开发期间不维护迁移脚本或历史回填。

开发中的表结构变化后，停止 Wave，清理其专用测试数据库及关联沙箱、文件数据，再运行 `wave init-db` 和 `wave bootstrap`。正常启动流程不得自动重置数据。

## 验证沙箱

在具备 Docker 和所选后端的宿主机上运行：

```bash
wave bench sandbox -backend gvisor -verify -concurrency 1,2 \
  -memory-mib 512,1024 -iterations 3 -format json > gvisor.json
wave bench sandbox -backend podman -verify -concurrency 1 \
  -memory-mib 1024 -iterations 1 -format json > podman.json
wave bench sandbox -backend sbx -memory-mib 1024 -concurrency 1 -iterations 1
```

压测使用临时 PostgreSQL，只清理自身创建的沙箱，不调用模型，容量预算独立于部署。容器后端的 `-verify` 检查执行、隔离、产物、去重、超时清理及磁盘保留。清理失败时，JSON 报告记录残留资源。

用 `-command` 测试实际负载。默认文件校验负载和镜像已缓存时的耗时不能预测 Agent 吞吐或内存峰值。参见[性能测试](../tools/bench/README.zh-CN.md)。
