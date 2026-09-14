# Wave 的日志、Trace 和压测

Wave 自己保存和查询执行遥测，不依赖 TraceRoot、Collector 或新的数据库。
任务、模型调用、工具调用和阶段事件共同组成 Trace；历史任务也可以查询已有计时。

## 按任务定位问题

```bash
# Wave API Key，不是模型供应商的 Key。避免把 Key 放进命令参数。
wave trace -key-file /path/to/wave-api-key -task task_xxx
wave trace -key-file /path/to/wave-api-key -task task_xxx -format json > trace.json
wave trace -key-file /path/to/wave-api-key -task task_xxx -format chrome > timeline.json
# 或使用用户 CLI / Go、Python、TypeScript SDK 的 executionTrace 操作
wavectl tasks trace --id task_xxx
```

`GET /v1/tasks/{id}/trace` 使用现有所有者鉴权。`trace_id` 就是根任务的 `root_id`；
子任务沿用同一个 trace ID，但有各自的 task/span ID。根任务返回子任务 span，
要检查子任务内部调用，用子任务 ID 查询同一个接口。

`timeline.json` 是 Chrome Trace Event 格式，可在本地支持该格式的时间线查看器打开。
它保留开始时间、持续时间、层级 ID 和未知结束状态，不会把中断调用伪造成瞬间完成。

- `wall_ms`：数据库记录的创建至完成时间；任务尚未完成时为 null。
- `initial_queue_ms`：创建至第一次执行，仅包含初始排队。
- `model_ms`：当前任务所有已知完整模型调用的总时长，包含压缩和重试；包含网络、供应商等待、生成及本地流处理。
- `first_delta_ms`：第一个模型增量的延迟，不等同于第一个可见正文 token；缺失为 null。
- `tool_ms`：已执行且有完整时间的工具调用总时长；外部结果等待和 `agent_wait` 单列为 `tool_wait`。
- `phase_ms`：顶层 `workspace.prepare` / `workspace.finish`。内部有 `sandbox.ensure`、`artifacts.publish`、`sandbox.stop` 等子 span；嵌套耗时不能重复相加。
- `unattributed_ms`：wall 减去已知区间的并集，包含未细分的调度、落库和后续等待；不能直接解释成 CPU 开销。缺失边界时为 null。
- `input_tokens` / `output_tokens`：当前任务各次模型调用分别累加，子任务不会重复计入。缓存 token 是输入的一部分，不能再加到输入总数上。`usage_known=false` 时是已知部分，不代表完整消费。

查询最多包含 5000 次模型调用、5000 次工具调用、10000 条阶段边界事件和 5000 个子任务。
超出会明确标记 `truncated`；没有完整边界会标记 `incomplete`。历史任务没有新增阶段记录，
它们的准备和收尾仍落在未归因时间内，不能反向补出精确沙箱耗时。

## 日志

服务默认向 stderr 写 JSON 日志，`WAVE_LOG_LEVEL=DEBUG|INFO|WARN|ERROR` 控制级别。
HTTP 响应包含 `X-Request-ID`；任务创建、查询和 Trace 查询还返回 `X-Wave-Trace-ID`。
HTTP、worker、模型和工具日志通过 `request_id`、`trace_id`、`session_id`、`task_id`、`span_id` 关联。
日志不采集请求体、提示词、模型输出、工具命令、凭证和原始异常文本；业务消息继续由原有鉴权 API 提供。

```bash
docker compose --project-directory deploy logs --no-log-prefix wave \
  | jq -c 'select(.trace_id == "task_xxx")'
```

Compose 为 Wave 的容器日志设置 10 MiB × 3 文件轮转；阶段数据沿用任务事件的生命周期，
不会额外启动日志服务。每个实际阶段记录开始和结束，模型增量不会逐 token 增加 Trace 写入。
阶段事件写入是带 250ms 超时和 worker fence 校验的尽力记录；失败会产生 `trace.write_failed`，
不会触发工具重试。只有开始记录的阶段会呈现未知结束；两条边界都丢失只能从警告日志和未归因时间发现。

## 真实模型 bench

```bash
cp tools/bench/example.live.json /tmp/case.json
# 替换 case 中的 agent_id；该 Agent 决定模型与工具权限。
wave bench live -case /tmp/case.json -key-file /path/to/wave-api-key \
  -tasks 10 -clients 1 -timeout 5m > baseline.json
wave bench live -case /tmp/case.json -key-file /path/to/wave-api-key \
  -tasks 10 -clients 1 -timeout 5m > candidate.json
wave bench compare -max-p95-regression 10 baseline.json candidate.json
```

live 模式访问已部署的 Wave，会创建真实会话和任务、消耗真实模型额度。
可用 `-url` 指定服务；模型 Key 沿用服务配置，bench 只需 Wave API Key。
每个任务独立会话，可在 case 的 `session` 中配置 `environment_id`、`file_ids`、`memory_store_ids`。
相同内存 store 可能发生写入竞争；用于性能对照时应使用固定、可重复的输入和环境。

必须配置至少一个确定性断言；支持 `result_equals`、`result_contains`，以及产物校验：

```json
{"expect":{"files":[{"path":"summary.json","json_equals":{"answer":42}}]}}
```

产物可用 `sha256` 校验精确字节；`json_equals` 忽略对象字段顺序但要求结构和值一致。
单个被校验产物限制 2 MiB。断言失败算 `invalid_result`，不能算压测成功。
报告不保存 case 的输入或任务输出，只保存工作负载哈希、Agent 版本、模型名、计时和 Trace 元数据。

任务超时、等待人工输入或异常时，bench 对已知未终止任务请求取消，并在报告记录
`cancel_requested` / `cancel_error`。取消请求成功不等于外部操作已经停止；`unknown` 状态
仍需按原有流程确认和 reconcile。会话和产物保留在服务中用于复查，不自动删除业务数据。

## 读报告与回归判断

保留原有 `wave bench`（本地合成模型、临时 PostgreSQL）与 `wave bench sandbox`。
合成 bench 的 v2 报告也保存任务 Trace，临时数据库清理后仍可分析。
live 与 synthetic 报告不可互相比；sandbox 报告使用原有格式。

报告包含 run ID、bench 二进制的 Git revision（无构建信息时为 unknown）、成功率、吞吐量、
p50/p95/p99、模型与工具分布、模型轮数、token 消耗和每个任务的 Trace。
live 模式的 Go/CPU 信息属于负载生成端，不代表服务端配置；服务端 worker 数无法从客户端推断，记录为 0。

端到端延迟包含创建会话、提交和状态轮询，截止到观察到结束，不包含后续断言与 Trace 下载；
闭环吞吐量包含这些检查的成本。`-poll` 同时影响观测延迟和 API 负载。
耗时分位数只统计结果校验通过的任务；失败率、未执行数、遥测缺失和样本数必须一起看。
`-warmup` 不进入测量分布，但仍会创建任务并消耗模型额度。

compare 要求相同 case 哈希、负载设置与 worker 阶段；失败、未完成、缺失 Trace 的运行不能通过。
p95 超过阈值时返回非零退出码，可接 CI。模型及 token 的变化同时展示。
少于 20 个成功样本会提示尾部分位数仅供探索；即使超过 20，也应重复测试，不能把单轮网络波动当成优化收益。
