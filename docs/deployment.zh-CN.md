# 部署

[English](deployment.md) | 简体中文

配置服务参数、安装 gVisor 沙箱后端、设置资源限额并初始化数据库。

## 配置服务

Compose 读取 `deploy/.env`，直接启动的进程读取环境变量。常用配置如下，完整示例见[配置文件](../deploy/.env.example)。

| 配置项 | 默认值 | 用途 |
| --- | --- | --- |
| `WAVE_WORKER_CONCURRENCY` | `10` | 每个进程并发的任务 worker 数 |
| `WAVE_CONTEXT_TOKENS` | `32000` | 上下文 token 预算，需保持在模型限制内 |
| `WAVE_DELTA_FLUSH_BYTES` | `4096` | 触发 `message.delta` 刷写的缓冲文本大小 |
| `WAVE_DELTA_FLUSH_MS` | `500` | 缓冲文本的最大停留时长，超过即刷写 |
| `WAVE_AUTH_CACHE_TTL_SEC` | `15` | `/v1` 热路径的 API 密钥正向缓存；密钥撤销跨副本在此窗口内收敛。`0` 表示禁用 |
| `WAVE_DB_MAX_OPEN_CONNS` | `32` | 数据库连接池上限；读多写少、API 客户端在池上排队时调高 |
| `WAVE_MODEL_TIMEOUT_SEC` | `300` | 模型请求超时（秒） |

按供应商限流和排队延迟调整并发。使用[性能压测](benchmarks.zh-CN.md)测量主机容量。对外访问使用 HTTPS，数据库和存储保持在私有网络。Wave 自身不限制请求速率；公开部署时应在前端放置具备限流能力的反向代理。

备份 `deploy/.env`、数据库、文件存储和沙箱数据。保存 `WAVE_MASTER_KEY` 和存储凭据；更换主密钥会使已加密的凭据无法读取。

## 选择文件存储

Wave 支持两种文件存储后端：

| 后端 | 存储位置 |
| --- | --- |
| `s3`（默认） | 任意兼容 S3 的服务；自带 compose 会启动 SeaweedFS |
| `local` | Wave 所在宿主机上的目录，由 `WAVE_DATA_DIR` 指定 |

S3 相关配置见[配置文件](../deploy/.env.example)。使用 `local` 时所有副本必须看到同一目录，因此只适合单机部署。

在 Docker 中，`WAVE_DATA_DIR` 默认为容器内的 `/var/lib/wave`，它不是卷：重建容器会丢弃其中的文件。在 `deploy/.env` 中加入下面一行即可启用 `deploy/compose.local.yaml`，把该路径放到 `local_data` 卷上：

```dotenv
COMPOSE_FILE=deploy/compose.yaml:deploy/compose.local.yaml
```

## 安装沙箱后端

| 后端 | 依赖 | 隔离方式 |
| --- | --- | --- |
| `gvisor`（默认） | Docker、使用 `systrap` 的 `runsc`；无需 KVM | 用户态内核 |

运行沙箱任务需要 gVisor。

在已安装 Docker 的 Ubuntu 主机上运行：

```bash
sudo bash deploy/install-sandbox-host.sh
```

脚本安装 runsc、注册 `wave-runsc`。脚本保留 Docker 配置并重载 daemon。为保证文件上传和文件系统写入持久化，runtime 必须包含以下参数：

```json
{"path":"/usr/bin/runsc","runtimeArgs":["--platform=systrap","--overlay2=none","--file-access=shared"]}
```

参见 [gVisor 文件系统说明](https://gvisor.dev/docs/user_guide/filesystem/)。

在 `deploy/.env` 中设置后端：

```dotenv
WAVE_SANDBOX_BACKEND=gvisor
WAVE_DOCKER_HOST=unix:///var/run/docker.sock
WAVE_GVISOR_RUNTIME=wave-runsc
WAVE_CONTAINER_IMAGE=docker.io/library/python:3.12-slim-bookworm
WAVE_CONTAINER_NETWORK=none
```

环境可将 `sandbox_backend` 设为 `gvisor`，也可省略：

```json
{"name":"python","sandbox_backend":"gvisor","sandbox_profile":"default"}
```

首次预留沙箱时，会话固定后端、引擎地址、镜像、CPU 和内存。后续配置修改只影响新会话。

## 设置资源限额

| 配置项 | 默认值 | 含义 |
| --- | --- | --- |
| `WAVE_SANDBOX_CPUS` | `1` | 默认客体 vCPU 数 |
| `WAVE_SANDBOX_MEMORY_MIB` | `1024` | 默认客体内存分配量 |
| `WAVE_SANDBOX_MAX_RUNNING` | `2` | 活动或预留沙箱数上限 |
| `WAVE_SANDBOX_MEMORY_BUDGET_MIB` | `4096` | 活动或预留沙箱的客体内存总量上限 |

两个容量限制同时生效，由共享数据库的副本共同执行。各副本必须使用相同引擎地址和限制。

默认 `/workspace` 位于容器可写层，没有磁盘配额，需自行监控保留磁盘的占用。设置 `WAVE_SANDBOX_WORKSPACE_GIB`（如 `8`）可将 `/workspace` 放到带容量上限的命名卷上。卷的 `size` 选项要求 Docker 宿主机的卷存储支持 ext4/XFS，不支持时会创建失败并报错；已创建的会话保持原有布局。其他数据库、手动启动的沙箱和压测不在此预算内。为 Wave、数据库、存储和沙箱运行时预留宿主机内存；客体分配量不等于 RSS。

创建环境时选择 `sandbox_profile`：

| 规格 | 分配量 | 工作负载 |
| --- | --- | --- |
| 省略或 `default` | 配置中的默认 CPU 和内存 | 脚本、文件处理 |
| `standard` | 2 vCPU / 2048 MiB | 安装依赖、构建、测试 |
| `large` | 2 vCPU / 4096 MiB | 浏览器、较大构建 |

缩减规格前先测试实际负载。gVisor 执行资源限制。修改规格不会调整已有沙箱，需新建会话。

同一会话的沙箱操作（执行、文件暂存）通过进程内锁串行化。会话同一时刻只有一个活动根任务，这由数据库在副本间强制执行；不同副本对同一会话并发进行文件暂存与任务执行时，只受 Docker 调用级并发的约束，不受 Wave 会话锁保护。

绑定环境的会话保留一个沙箱，由同一会话的任务和子 Agent 共享。根任务正常收尾时停止计算、保留磁盘。等待审批、未知执行结果或收尾失败时，可能继续占用计算资源和额度。Worker 租约到期不会释放沙箱预留。

容量满时，新任务留在队列中，不消耗执行超时。取消任务和复用已有沙箱的子任务仍可继续。单个规格超过总内存预算时准入失败；降低预算不会驱逐运行中的沙箱。Worker 数与沙箱容量分别限制。

## 选择镜像

容器后端默认使用 `python:3.12-slim-bookworm`，包含 Bash、Python 和 pip。需要 Node.js、npm 和 Git 时构建：

```bash
docker build -f deploy/sandbox.Dockerfile -t wave-ai-sandbox:local .
```

设置 `WAVE_CONTAINER_IMAGE=wave-ai-sandbox:local`。固定镜像 digest 可保证会话可复现；保存 `latest` 引用不会固定镜像内容。

离线执行需预装工具和依赖。Alpine 可能需要适配 Bash、apt 或 glibc；distroless 缺少所需 shell。

## 文件与网络

容器沙箱不接收引擎 socket 或 Wave 凭据。上传文件和技能使用只读卷，SDK 通过停止状态的 staging 容器写入。

容器命令限制输出、CPU、内存、PID 和 capabilities，并启用 no-new-privileges。沙箱之外，Wave 引擎本身以 root 运行并持有 Docker socket（需要创建 gVisor 容器）；沙箱容器在 `CapDrop: ALL` 之外额外获得六个能力（`CHOWN`、`DAC_OVERRIDE`、`FOWNER`、`FSETID`、`SETUID`、`SETGID`）以保证文件暂存可用。应将引擎主机视为特权主机，Docker socket 仅供 Wave 使用。超时会停止整个会话容器，包括后台进程。已完成的请求 ID 复用结果，结果不明的执行不会重放。

网络默认为 `none`。需要在线安装依赖时，配置专用引擎网络及出口策略；选择网络本身不会过滤目标地址。不允许 host 或共享容器网络。保留磁盘的占用需单独监控，不受内存预算限制。

Wave 通过 Docker Go SDK 访问 Docker。容器适配器不调用引擎 CLI；宿主机安装和临时压测数据库使用管理 CLI。

## 初始化数据库

`wave init-db` 在空数据库中创建当前表结构。重复运行保留数据，不升级已有表。开发期间不维护迁移脚本或历史回填。

开发中的表结构变化后，停止 Wave，清理其专用测试数据库及关联沙箱、文件数据，再运行 `wave init-db` 和 `wave bootstrap`。正常启动流程不得自动重置数据。

## 验证沙箱

在具备 Docker 和所选后端的宿主机上运行：

```bash
wave bench sandbox -backend gvisor -verify -concurrency 1,2 \
  -memory-mib 512,1024 -iterations 3 -format json > gvisor.json
```

用 `-command` 测试实际负载。默认文件校验负载和镜像已缓存时的耗时不能预测 Agent 吞吐或内存峰值。`-verify` 的检查项、完整参数和 `make bench-sandbox` 用法见[性能压测](benchmarks.zh-CN.md)。
