# 日志、Trace 与指标

[English](OBSERVABILITY.md) | 简体中文

## 查询任务 Trace

使用 Wave API 密钥，不使用模型供应商密钥：

```bash
wave trace -key-file /path/to/wave-api-key -task TASK_ID
wave trace -key-file /path/to/wave-api-key -task TASK_ID -format json > trace.json
wave trace -key-file /path/to/wave-api-key -task TASK_ID -format chrome > timeline.json
wavectl tasks trace --id TASK_ID
```

`GET /v1/tasks/{id}/trace` 按所有者鉴权。根任务的 `root_id` 为 trace ID，子任务沿用该 ID，但有独立的 task 和 span ID。查询子任务可查看其内部调用。`timeline.json` 可在支持 Chrome Trace Event 格式的查看器中打开。

| 字段 | 定义 |
| --- | --- |
| `wall_ms` | 创建至完成；未完成时为 null |
| `initial_queue_ms` | 创建至首次执行 |
| `model_ms` | 当前任务已完整计时的模型调用，包含重试、压缩、网络和流处理 |
| `first_delta_ms` | 首个模型增量延迟，不一定是首个可见正文 token |
| `tool_ms` | 已执行且计时完整的工具调用；外部结果和 `agent_wait` 等待另记为 `tool_wait` |
| `phase_ms` | 顶层 `workspace.prepare` 和 `workspace.finish`，不要重复加上嵌套 Span |
| `unattributed_ms` | Wall 时间减去已知区间的并集，包含调度、持久化等，不只是 CPU 开销 |
| `input_tokens`、`output_tokens` | 当前任务模型用量，不重复累计子任务；缓存 token 是输入的子集 |

缺失边界保持未知，不补零。`usage_known=false` 表示用量不完整。每次查询最多包含 5,000 次模型调用、5,000 次工具调用、10,000 条阶段边界和 5,000 个子任务；检查 `truncated` 和 `incomplete`。历史任务无法补回未记录的阶段计时。

## 查询日志

Wave 向 stderr 写 JSON 日志。`WAVE_LOG_LEVEL` 支持 `DEBUG`、`INFO`、`WARN`、`ERROR`。HTTP 响应包含 `X-Request-ID`；任务创建、任务查询和 Trace 查询还返回 `X-Wave-Trace-ID`。

```bash
docker compose --project-directory deploy logs --no-log-prefix wave \
  | jq -c 'select(.trace_id == "TASK_ID")'
```

日志通过 `request_id`、`trace_id`、`session_id`、`task_id` 和 `span_id` 关联。[后台](../../web/README.zh-CN.md)的 **Logs** 支持搜索并跳转到对应 Span。任务和会话的执行事件单独展示。

只持久化具有所有者上下文的 HTTP 和 worker 日志。启动、维护及成功的后台查询只写 stderr，避免每次监控刷新都产生持久化日志。保存字段包括消息、级别、模块、关联 ID、允许的元数据和安全错误码，不包含提示词、模型输出、工具命令、凭据或原始异常。

日志队列容量为 4,096 条，每批最多 128 条或等待 250 ms。失败批次重试；队列满时丢弃新日志，并输出 `monitor.logs_dropped`。进程崩溃可能丢失队列中的日志。Compose 按 10 MiB × 3 文件轮转 stderr 日志。

阶段写入有 250 ms 超时及 worker fence 校验；失败输出 `trace.write_failed`，不会重试工具。缺少结束边界时 Span 不完整；两条边界都缺失时，需检查警告和未归因时间。阶段数据沿用任务事件的保留周期，模型增量不逐 token 创建 Span。

## 查看指标

在 **Metrics** 中选择范围和图表时间点，查看该时段的 Trace、失败任务或日志。任务筛选使用完成时间。

| 指标 | 定义 |
| --- | --- |
| 任务吞吐 | 每分钟完成任务数，根任务和子任务分别计数 |
| 成功率 | succeeded / 全部完成任务，分母包含 failed、partial、canceled |
| 任务延迟 | 创建至完成，展示 P50/P95/P99 |
| 模型首增量 | 第一个模型增量的延迟，展示 P50/P95 |
| Token 用量 | 逐次累计输入、输出及缓存输入；缓存输入不重复相加 |
| 队列等待 | 仅初次排队 |
| 沙箱启动 | `sandbox.ensure` 耗时，包含复用检查 |
| 模型延迟 | 模型调用耗时，包含重试和压缩 |

未知用量和中断调用的未知耗时不补零。无样本的延迟和成功率显示 `—`。分位数由桶宽为 2% 的对数直方图合并估算，不对各桶分位数求平均。

指标持续聚合为分钟桶和小时桶，查询不扫描历史 Trace 或任务。标签仅使用固定指标名和所有者范围，不包含 task、session 或 model ID。任务和模型样本随状态变更提交；沙箱样本依赖尽力写入的阶段记录。多个副本通过事务更新桶并删除已消费样本。

| 数据 | 保留时间 |
| --- | --- |
| 结构化日志 | 7 天 |
| 分钟桶 | 48 小时 |
| 小时桶 | 31 天 |

最多查询最近 30 天，时间范围向外对齐至桶边界。待聚合样本保留至处理完成；后台提示超过五秒的聚合延迟。指标从启用采集时开始，不从旧事件回填。表结构变化时参见[数据库初始化](../../deploy/sandbox-resources.zh-CN.md#初始化数据库)。

## 测试真实模型

使用已有 Agent 准备 case，模型和工具由该 Agent 决定：

```bash
cp tools/bench/example.live.json /tmp/case.json
# 设置 /tmp/case.json 中的 agent_id。
wave bench live -case /tmp/case.json -key-file /path/to/wave-api-key \
  -tasks 10 -clients 1 -timeout 5m > baseline.json
wave bench live -case /tmp/case.json -key-file /path/to/wave-api-key \
  -tasks 10 -clients 1 -timeout 5m > candidate.json
wave bench compare -max-p95-regression 10 baseline.json candidate.json
```

Live 模式创建真实会话和任务，消耗模型额度。`-url` 指定服务，供应商凭据沿用服务配置。每个任务使用独立会话；case 的 `session` 可配置 `environment_id`、`file_ids` 和 `memory_store_ids`。共享 memory store 可能产生写入竞争。

Case 必须包含确定性断言：`result_equals`、`result_contains` 或产物检查：

```json
{"expect":{"files":[{"path":"summary.json","json_equals":{"answer":42}}]}}
```

`sha256` 校验精确字节，`json_equals` 校验结构和值、忽略对象字段顺序。单个被检产物上限为 2 MiB。断言失败计为 `invalid_result`。报告保存工作负载哈希、Agent 版本、模型名称、计时和 Trace 元数据，不保存任务输入输出。

超时或需要人工动作时，压测请求取消，并记录 `cancel_requested` 或 `cancel_error`。取消不代表外部执行已停止；`unknown` 结果需按[恢复流程](../../skills/wave-client/references/recovery.md)核实。会话和产物保留供复查。

## 比较报告

合成压测和 Live 压测生成含任务 Trace 的 v2 报告，沙箱报告使用独立格式。不同模式不能互比。

报告包含代码版本、成功率、吞吐、P50/P95/P99、模型与工具耗时、轮数和 token 用量。Live 的 Go/CPU 信息属于负载生成端，服务端 worker 数未知，记为 0。

端到端延迟包含创建会话、提交和轮询至观察到完成，不包含断言和 Trace 下载；吞吐计时包含这些检查。`-poll` 影响延迟和 API 负载。`-warmup` 不计入测量分布，但仍创建任务并消耗模型额度。

对比要求 case 哈希、负载配置和 worker 档位一致。有失败、未完成任务或缺失 Trace 的运行不能通过。P95 回退超过 `-max-p95-regression` 时返回非零退出码。延迟分位数只统计校验成功的任务，需同时查看失败、未执行任务和样本数。少于 20 个成功样本时，尾部分位数仅供探索；重复运行后再判断优化效果。

合成与沙箱测试见[压测指南](README.zh-CN.md)。
