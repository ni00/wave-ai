# Wave 性能压测

[English](README.md) | 简体中文

测量目标主机上的任务吞吐、延迟和沙箱性能。

| 模式 | 测量范围 | 依赖 |
| --- | --- | --- |
| `wave bench` | 真实 HTTP API、worker、PostgreSQL 和本地模拟模型 | Docker CLI 与本机 Docker daemon |
| `wave bench sandbox` | 沙箱创建、命令执行、停止和重启 | Docker 与选定的沙箱后端 |

无需现有 Wave 部署或模型密钥。以下 Make 命令在仓库根目录执行，需要 Go 1.27+；已有二进制时可直接运行 `wave bench`。服务镜像不包含 Docker CLI，请在宿主机运行。

## 1. 运行基础压测

```bash
make bench
make bench BENCH_ARGS='-workers 2,5,10,20 -tasks 500 -clients 32 -warmup 20 -timeout 5m'
make -s bench BENCH_ARGS='-format json' > bench.json
```

基础模式使用临时数据库和本地模拟模型，忽略部署的 `WAVE_*` 配置，无需 `make setup`。

### 1.1 参数

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `-workers` | `2,5,10,20` | 依次测试的服务端 worker 数，不修改部署配置 |
| `-clients` | `32` | 最大在途任务数；实际还受任务总数限制 |
| `-tasks` | `100` | 每档计入统计的任务数 |
| `-warmup` | `10` | 每档额外预热任务数；可设为 0 |
| `-model-delay` | `100ms` | 每次模型流式响应的总模拟等待，分摊至每个 chunk |
| `-poll` | `100ms` | 客户端轮询任务状态的间隔 |
| `-timeout` | `2m` | 每轮预热、每轮正式测试各自的总时限，包含排队 |
| `-format` | `human` | `human` 表格或 `json` 完整报告 |

模拟模型只输出文本，token 用量为合成数据。`-model-delay 0` 侧重服务开销；较长延迟（如 `2s`）用于观察模型等待下的并发扩展。响应大小等其他参数见 `wave bench --help`。

### 1.2 负载与计时

每档 worker 数使用全新数据库。每个客户端等当前任务完成后再提交下一个，每个任务使用独立会话。计时包含创建会话、提交任务和状态轮询，不包含建表与预热。

### 1.3 指标

| 指标 | 口径 |
| --- | --- |
| `successful_tasks_per_second` | 成功任务数 / 正式测试总时长 |
| `error_rate` | 失败任务数 / 已尝试任务数；包含 HTTP 错误和超时 |
| `end_to_end` | 开始创建会话至观察到任务完成，包含轮询延迟 |
| `queue` | 服务端 `started_at - created_at` |
| `execution` | 服务端 `finished_at - started_at`，包含模型等待与持久化 |
| `process_peak_sampled_go_heap_mib` | 每 100 毫秒采样整个 Go 进程的堆峰值，包含 Wave、压测客户端、模拟模型和统计 |
| `db_pool_wait_count`、`db_pool_wait_ms` | 正式测试期间获取数据库连接的累计等待次数与时长；连接池上限为 32 |

延迟只统计成功任务，使用 nearest-rank 百分位算法。`count: 0` 表示无有效样本；`requested - attempted` 表示未尝试的任务数。堆采样不包含 PostgreSQL，也不代表进程 RSS 或整机内存峰值。

报告写入 stdout，进度写入 stderr。失败、超时或未完成的阶段返回非零退出码，保留已有统计，并停止后续阶段。

## 2. 比较结果

1. 固定客户端数、模型参数、轮询间隔和任务数，只改变 worker 数。客户端太少也会限制吞吐。
2. 比较时使用至少 500 个任务并重复运行，减少尾部延迟估计的波动。
3. 同时观察吞吐、P95/P99、失败率和数据库连接等待。吞吐不再增长而延迟持续上升时，增加 worker 的收益已有限。
4. 保存 JSON 报告，并记录代码版本、主机规格和其他负载。单独监控 CPU、内存、磁盘和容器用量。

基础压测不覆盖真实供应商的限流与网络、沙箱、浏览器、文件传输、S3、调度器、SSE 订阅者或多轮长上下文。结果用于比较主机和版本，不能直接换算成真实用户数或沙箱容量。

## 3. 运行沙箱压测

先按[沙箱部署指南](../../deploy/sandbox-resources.md)准备后端，再执行：

```bash
make bench-sandbox
make bench-sandbox BENCH_ARGS='-backend gvisor -verify -concurrency 1,2'
```

支持 gVisor（默认）、Podman 和 sbx，读取对应后端配置。测试不受现有部署的沙箱容量上限约束，应在专用测试主机上运行；不测量宿主机 RSS 或客体内存峰值。

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `-backend` | `gvisor` | 被测沙箱后端：`gvisor`、`podman` 或 `sbx` |
| `-verify` | `false` | 校验执行结果，而不只是计时 |
| `-concurrency` | `1,2` | 依次测试的并发档位 |
| `-memory-mib` | `512,1024,2048` | 依次测试的客体内存规格 |
| `-iterations` | `3` | 每个客户端在每种内存规格下新建沙箱的次数 |
| `-timeout` | `3m` | 每个样本的时限，不含强制清理 |

镜像、自定义命令等参数见 `wave bench sandbox --help`。

## 4. 清理与验证

正常退出或 Ctrl-C 后自动清理测试资源。异常终止后，按启动日志中的容器名执行 `docker rm -fv <容器名>` 清理残留容器。

修改压测代码后，运行 `make bench-test`，执行需要 Docker 的竞态检测与集成测试。
