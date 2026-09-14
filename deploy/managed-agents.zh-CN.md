# 配置托管执行

[English](managed-agents.md)

## 定时运行

1. 打开 **Deployments**，选择 Agent 和可选 Environment。
2. 填写五段 cron 表达式和 IANA 时区。cron 留空时仅手动运行。
3. 设置每次运行的预算，绑定 Files、Memory 和 Vaults。Files 和 Memory 需要 Environment。
4. 保存，检查未来五次触发时间和固定的 Agent 版本。

编辑会创建配置版本。已有任务保留 Agent 快照。启用“每次使用最新 Agent 版本”后，每次触发时读取当前版本。暂停只停止定时触发，仍可手动运行。

默认使用 UTC，跳过迟到超过 30 秒的触发，并在上一任务未结束时跳过新运行。`run_once` 在错过触发后补跑一次，不逐次回放历史计划。`allow` 允许在独立会话中重叠运行。

每次触发记录 `launched`、`skipped` 或 `failed`。资源不存在或已归档时，部署自动暂停。修正配置后再恢复。手动请求可使用 `Idempotency-Key` 去重；相同请求重试时复用同一个键。

## 委派专家

保存 Agent 时固定专家版本。更新专家不会改变已有协调者；在编辑器中点击“更新专家到当前版本”才能采用新配置。

`delegation_policy` 默认是 `intersection`，专家只能使用双方共同授权的能力。`explicit` 允许选中的专家使用自身工具和 Skills。专家不能再次委派。子任务共享 Session 沙箱和根任务预算，各自保留消息历史。

协调者可以向 `agent_wait` 传入：

```json
{"task_ids":["task_example"],"mode":"any","timeout_seconds":60}
```

省略 `task_ids` 时等待当前全部子任务；`mode` 默认为 `all`。超时范围为 0–3600 秒，0 表示不单独设置等待期限，任务时长预算仍然生效。协调者仍在运行时，`agent_message` 可以恢复已结束的子任务。

在 **Task → 协作**、**Tools** 和 **Inputs** 中查看工作并介入。可以批准或拒绝调用、提交自定义工具的实际结果、发送输入或取消任务。确认未知执行已停止前，应先核查沙箱和工具状态。

## 绑定凭据

1. 创建 **Vault**，添加精确主机名和 bearer token。
2. 在 Agent 的 MCP 工具中引用凭据 ID。
3. 将 Vault 绑定到 Session 或 Deployment。

Token 加密存储，API 不返回明文。轮换需要当前凭据版本，新 token 用于后续调用。归档 Vault 或吊销凭据会阻止后续调用。“校验绑定”仅检查目标和本地加密数据，不联系服务商，也不验证 token 在服务商处是否有效。

未归入 Vault 的已有凭据继续使用直接凭据 ID 绑定。目前支持静态 MCP bearer token，不支持 OAuth 刷新或沙箱密钥注入。

## 接收事件

创建 **Webhook**，填写 HTTPS URL、事件类型和 32–1024 字节签名密钥。URL 不允许内嵌凭据、查询参数或片段。投递阻止私有网络目标和重定向。

事件正文只包含事件、任务、会话标识和状态。通过认证 API 获取内容。投递至少执行一次；接收方应按 `Wave-Event-ID` 去重。

使用签名密钥对 `Wave-Timestamp + "." + 原始正文` 计算 HMAC-SHA256。`Wave-Signature` 为 `v1=` 加十六进制摘要。接收方应拒绝过期时间戳，并使用恒定时间比较签名。

HTTP 2xx 表示接收成功。其他响应和连接失败最多尝试八次，最后一次尝试前的指数退避间隔为 2–128 秒。在 **Webhooks → 投递记录** 中查看并手动重试失败投递。暂停后不再捕获新事件；待投递记录被领取时进入暂停，恢复订阅后继续。暂停期间的事件不会补发。

## 验收产出

每个 Agent 最多配置 20 条验收规则：

```json
[{"kind":"json","path":"summary.json","required":["total"],"types":{"total":"number"}}]
```

`file_exists` 检查已发布文件是否存在。`json` 检查 JSON 对象、必需的顶层字段和可选字段类型。路径相对于 `/mnt/session/outputs`；省略路径时检查最终回复。每条规则读取的 JSON 上限为 1 MiB。

**Task → 验收** 单独显示 `passed`、`failed` 或 `error`。根任务成功或部分完成并发布文件后执行检查，不自动重跑 Agent。Benchmark 报告包含验收覆盖数和通过率；基线包含检查时，结果对比会拒绝覆盖不足或通过率下降的候选结果。
