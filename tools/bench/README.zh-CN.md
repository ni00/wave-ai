# Wave bench

[English](README.md) | 简体中文

`wave bench` 在目标机器上运行独立的端到端基础压测。它启动本机临时 PostgreSQL 16 容器、Wave HTTP API 和实际 worker，并用确定性的本地流式模型代替外部供应商。无需现有部署、模型账号、存储账号或沙箱。

## 运行

```bash
make bench
make bench BENCH_ARGS='-workers 2,5,10,20 -tasks 500 -clients 32 -warmup 20 -timeout 5m'
make -s bench BENCH_ARGS='-workers 2,5,10,20 -format json' > bench.json
make bench-test
```

也可构建后在宿主机直接运行 `wave bench`。宿主机需要可用的本机 Docker daemon，首次运行可能拉取 `postgres:16-alpine`；不需要 Compose、`make setup` 或现有服务。服务镜像没有 Docker CLI，本工具应在宿主机执行。

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `-workers` | `2,5,10,20` | 依次测试的服务端 worker 数，不修改部署配置 |
| `-clients` | `32` | 最大在途任务数；实际还受任务总数限制 |
| `-tasks` | `100` | 每档计入统计的任务数 |
| `-warmup` | `10` | 每档额外预热任务数；可设为 0 |
| `-model-delay` | `100ms` | 每次模型流式响应的总模拟等待，分摊至每个 chunk |
| `-chunks` | `8` | 模拟响应的 chunk 数 |
| `-chunk-bytes` | `128` | 每个 chunk 的 ASCII 字节数 |
| `-poll` | `100ms` | 客户端轮询任务状态的间隔 |
| `-timeout` | `2m` | 每轮预热、每轮正式测试各自的总时限，包含排队 |
| `-format` | `human` | `human` 表格或 `json` 完整报告 |

响应最多 4096 个 chunk，总大小不超过 1 MiB。模拟模型只输出文本，不会调用任何工具。`-model-delay` 为 0 时更偏向测量调度、HTTP 和数据库开销；2s 等较长等待可观察模型 I/O 占主导时的 worker 扩展效果。模型不真正进行 tokenization，usage 仅为合成数据。

## 负载与指标

每档先初始化全新数据库，再预热，然后开始计时。每个客户端完成一个任务才提交下一个，这是闭环并发负载。每个任务建立独立 Session，避免同一 Session 的串行规则限制并发。计时包含 Session 创建、任务提交和状态查询，排除建表和预热。

- `successful_tasks_per_second`：成功任务数 / 整轮实测时间，失败任务不贡献吞吐。
- `error_rate`：失败任务数 / 已尝试任务数；超时和客户端 HTTP 错误也算失败。`requested` 与 `attempted` 的差值是未尝试的任务。
- `end_to_end`：客户端开始创建 Session 至观察到任务完成，包含轮询延迟。
- `queue`：服务端 `started_at - created_at`。
- `execution`：服务端 `finished_at - started_at`，包含模拟模型等待与持久化。
- 延迟分布只含成功任务，使用 nearest-rank 百分位；JSON 的 `count` 为 0 表示无有效样本。100 个任务的 P99 非常粗糙，正式比较建议至少 500 个任务并重复运行。
- `process_peak_sampled_go_heap_mib`：每 100ms 采样整个 Go 进程的堆，包括 Wave、压测客户端、模拟模型和统计；不是 RSS，不包含 PostgreSQL，也不代表整机峰值。
- `db_pool_wait_count` / `db_pool_wait_ms`：正式测试期间，Wave 数据库连接池获取连接的累计等待次数和时间；当前使用服务原有的最大 32 连接设置。

stdout 仅输出报告，进度写 stderr。任务失败、未完整执行或超时会返回非零退出码，并保留已完成阶段与当前阶段的可用统计；首个失败阶段后停止。不要把所有任务失败时的 0ms 当成好成绩。

## 解释结果

固定 clients、模型参数、轮询间隔及任务数比较 worker 档位；clients 太少会限制吞吐。吞吐趋于平稳而延迟、连接池等待继续上涨时，增加 worker 的收益已经有限。另用宿主机监控查看 CPU、内存、磁盘和容器使用量，在 VPS 上尤其关注其他租户导致的波动。对比不同机器或代码版本时保留 JSON，并记录提交版本、VPS 规格和其他运行负载。

本工具没有覆盖真实模型限流/网络、沙箱/浏览器、文件上传下载、SeaweedFS/S3、scheduler、SSE 订阅者和多轮长上下文；模拟模型虽然发送 SSE，但压测客户端通过轮询观察任务。结果不能换算成“保证支持多少真实用户”或“能运行多少沙箱”。

工具忽略部署的 `WAVE_*` 变量，测试 PostgreSQL 只绑定 `127.0.0.1` 随机端口；每次运行有独立容器名，退出或 Ctrl-C 后清理容器、匿名卷和临时目录。进程被 SIGKILL、机器断电或 Docker 无法响应时，清理可能无法完成；启动日志提供容器名，可用 `docker rm -fv <容器名>` 清理。
