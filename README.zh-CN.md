# Wave AI

[English](README.md) | 简体中文

Wave AI 是可自行部署的 Agent 服务。通过 API 定义 Agent、提交任务和查询执行状态。

## 主要能力

- **任务恢复。** 保存执行进度，重启后恢复可继续的任务；工具结果不明时先确认再恢复。
- **工具审批。** 在关键操作前等待审批，或由客户端执行工具并提交结果。
- **进度跟踪。** 通过 SSE 或轮询读取事件，断线后凭游标续读。
- **沙箱执行。** 隔离运行 shell 和文件工具，任务结束后停止计算、保留文件。
- **文件、技能与记忆。** 为 Agent 提供输入文件、可复用技能和跨任务记忆，收集执行产物。
- **版本与调度。** 固定任务使用的 Agent 版本，按 cron 定时执行。

## 快速开始

需要 Docker Compose、Make、OpenSSL，以及兼容 OpenAI Chat Completions API 的模型服务。以下命令均在仓库根目录执行。

1. 生成部署配置：

   ```bash
   make setup
   ```

2. 编辑 `deploy/.env`，设置 `WAVE_DB_PASSWORD`、`WAVE_MODEL_BASE_URL` 和 `WAVE_MODEL_API_KEY`。模型端点须支持流式 `POST /chat/completions` 请求。

3. 启动服务并创建 Wave API 密钥：

   ```bash
   make up
   docker compose --project-directory deploy exec wave wave bootstrap -org demo -user admin
   ```

   保存输出的密钥，它只显示一次。客户端通过 **Wave API 密钥**访问 Wave；`WAVE_MODEL_API_KEY` 则用于 Wave 访问模型供应商。

4. 打开 [Swagger UI](http://localhost:8080/swagger/index.html)，在 **Authorize** 中填写 Wave API 密钥，无需 `Bearer` 前缀。

`make down` 停止容器并保留数据卷。数据库初始化只在空库中创建当前表结构，项目不提供历史迁移；切换版本前参见[数据库初始化说明](deploy/sandbox-resources.zh-CN.md#初始化数据库)。

### 提交任务

以下 Bash 示例需要 `curl` 和 `jq`。将 `WAVE_API_KEY` 设为 `bootstrap` 输出的密钥，并将 `YOUR_MODEL` 替换为供应商支持的模型。

```bash
BASE=http://localhost:8080
auth=(-H "Authorization: Bearer ${WAVE_API_KEY:?Set WAVE_API_KEY first}" \
  -H 'Content-Type: application/json')

agent=$(curl -fsS "$BASE/v1/agents" "${auth[@]}" \
  -d '{"name":"assistant","model":"YOUR_MODEL"}' | jq -er .id)

session=$(curl -fsS "$BASE/v1/sessions" "${auth[@]}" \
  -d '{"title":"first run"}' | jq -er .id)

task=$(curl -fsS "$BASE/v1/sessions/$session/tasks" "${auth[@]}" \
  -H "Idempotency-Key: first-run-$session" \
  -d "$(jq -n --arg agent "$agent" '{agent_id: $agent, input: "Say hello"}')" | jq -er .id)

curl -fsS "$BASE/v1/tasks/$task" "${auth[@]}" | jq '{id,state,result}'
```

创建任务返回 HTTP 202，任务在后台异步执行。重复最后一条请求可查看进度，也可使用 [CLI](cli/README.zh-CN.md) 或 SDK 等待完成。客户端超时后，继续查询已有任务 ID；重试同一次提交时，复用原幂等键。

## 执行模型

| 概念 | 用途 |
| --- | --- |
| Agent | 定义模型、指令、工具和技能；配置按版本管理。 |
| 会话（Session） | 保存对话和工作上下文，同一时刻只允许一个活动根任务。 |
| 任务（Task） | Agent 的一次执行，使用固定版本，受 token、工具调用和时长等预算约束。 |
| 运行环境（Environment） | 配置沙箱后端、资源规格和软件包。 |
| 定时部署（Deployment） | 配置 Agent 的执行计划，记录每次运行结果。 |

任务进入 `waiting` 时可能需要审批或工具结果；进入 `unknown` 时需核实执行情况。通过 CLI 或 SDK 查询待处理动作，按[工具处理](skills/wave-client/references/tools.md)与[恢复流程](skills/wave-client/references/recovery.md)继续。

## 部署

### 模型与并发配置

Compose 读取 `deploy/.env`，直接启动的进程读取环境变量。常用配置如下，完整列表见[配置示例](deploy/.env.example)。

| 配置项 | 默认值 | 用途 |
| --- | --- | --- |
| `WAVE_WORKER_CONCURRENCY` | `10` | 每个进程的任务执行并发数 |
| `WAVE_CONTEXT_TOKENS` | `32000` | 上下文 token 预算，不应超过模型限制 |
| `WAVE_MODEL_TIMEOUT_SEC` | `300` | 模型请求超时，单位为秒 |

根据模型供应商限额和排队延迟调整并发，可用[压测工具](tools/bench/README.zh-CN.md)测量主机容量。对外访问使用 HTTPS，数据库和存储放在私有网络中。

### 沙箱与备份

默认使用 Docker + gVisor，也支持 rootless Podman 和 sbx。运行沙箱任务前，按[沙箱部署指南](deploy/sandbox-resources.zh-CN.md)准备宿主机。`local` 后端以服务用户的宿主机权限执行命令，仅适用于可信开发环境。

每个沙箱默认分配 1 vCPU、1 GiB 内存；所有副本共享 2 个活动或预留沙箱、合计 4 GiB 客体内存的默认上限。沙箱配额与任务并发分别配置。

备份 `deploy/.env`、数据库、文件存储和沙箱数据。保留 `WAVE_MASTER_KEY` 及存储凭据；更换主密钥会导致已有加密凭据无法解密。

不使用 Compose 时，参阅[本机启动说明](skills/wave-admin/references/setup.md)。

## 客户端与文档

| 用途 | 文档 |
| --- | --- |
| 管理资源、跟踪任务 | [wavectl](cli/README.zh-CN.md) |
| 集成到应用 | [Go SDK](sdks/go/README.zh-CN.md)、[Python SDK](sdks/python/README.zh-CN.md)、[TypeScript SDK](sdks/typescript/README.zh-CN.md) |
| 为外部 Agent 提供 Wave 操作流程 | [wave-client 技能](skills/wave-client/SKILL.md) |
| 部署与运维 | [wave-admin 技能](skills/wave-admin/SKILL.md) |
| 查询 API 请求与响应 | [英文 OpenAPI](api/openapi.json)、[中文 OpenAPI](api/openapi.zh-CN.json) |
| 测量运行时、模型与沙箱性能 | [压测指南](tools/bench/README.zh-CN.md) |
| 查询日志、Trace 和指标 | [可观测性](tools/bench/OBSERVABILITY.zh-CN.md) |
| 使用 Web 后台 | [后台指南](web/README.zh-CN.md) |

Swagger UI 支持中英文切换，也可直接打开[中文界面](http://localhost:8080/swagger/index.html?lang=zh-CN)。

## 开发

```bash
make build                       # 构建服务。
make test                        # 运行单元测试和架构测试。
make check                       # 检查契约、运行 vet、校验模块并构建。
make docs clients-generate       # 重新生成 API 文档和客户端。
make clients-check clients-test  # 检查生成一致性与客户端行为。
```

依赖安装、集成测试和打包见[客户端工具链](tools/clients/README.zh-CN.md)。

[配置定时运行、专家协作、Vaults、Webhooks 和验收规则](deploy/managed-agents.zh-CN.md)。
