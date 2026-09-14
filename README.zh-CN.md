# Wave AI

[English](README.md) | 简体中文

Go 编写的托管 Agent 服务，支持持久任务、工具审批、流式事件、文件存储和可选沙箱。同一二进制提供 API、worker 和 scheduler 角色。

## 快速启动

需要 Docker Compose、`make` 和 OpenSSL。在仓库根目录执行：

```bash
make setup
# 编辑 deploy/.env：设置数据库密码、模型 API 地址与 Key，以及下方推荐参数。
make up
docker compose --project-directory deploy exec wave wave bootstrap -org demo -user admin
```

保存 `bootstrap` 显示的 Wave API Key，它与模型供应商 Key 独立。API 默认监听 8080；`make down` 停止容器并保留数据卷。首次初始化使用空数据库，项目不提供历史结构迁移。

## 推荐配置

适用于调用外部模型 API 的小规模单机部署：

| 项目 | 起始建议 |
| --- | --- |
| 主机 | 4 vCPU、8 GiB 内存、50 GB SSD；按文件保留量扩容磁盘 |
| 服务 | 使用自带 Compose：Wave、PostgreSQL 16、SeaweedFS（S3） |
| Worker 并发 | 从 2 开始，结合排队延迟、内存和模型供应商限额调整 |
| 沙箱 | 可选；默认分配 1 vCPU / 1 GiB；活动及预留沙箱默认最多 2 个，客体内存合计最多 4 GiB |
| 对外访问 | 使用 HTTPS 反向代理，数据库和存储留在私有网络 |

这些是未经容量压测的起始估算；本地模型和沙箱资源另计。在 `deploy/.env` 中覆盖以下参数：

```dotenv
WAVE_WORKER_CONCURRENCY=2
WAVE_CONTEXT_TOKENS=32000
WAVE_MODEL_TIMEOUT_SEC=300
WAVE_LEASE_SECONDS=30
```

创建 Agent 时选择供应商支持的模型，上下文预算不要超过模型限制。无环境任务不需要沙箱；默认沙箱为 Docker + gVisor，也支持 sbx 和 rootless Podman；先按[沙箱配置说明](deploy/sandbox-resources.md)准备宿主机。`local` 沙箱仅用于可信开发。

沙箱容量与 worker 并发独立控制。详见[沙箱规格、环境分档与初始化说明](deploy/sandbox-resources.md)；开发期间直接清空旧测试数据并初始化当前表结构。

保留 `make setup` 生成的存储凭据与 `WAVE_MASTER_KEY`，备份 `.env`、PostgreSQL、文件和沙箱数据；更换主密钥会导致已有凭据无法解密。[完整配置示例](deploy/.env.example)供查阅。Compose 读取 `deploy/.env`，本机进程只读取环境变量。

不使用 Compose 的本机开发需要 Go 1.27+、PostgreSQL 16+，可设置 `WAVE_STORAGE_BACKEND=local`，详见[启动说明](skills/wave-admin/references/setup.md)。

## 性能压测

在目标服务器上运行 `make bench`（需要本机 Docker 和 Go）；已有 `wave` 二进制时可直接运行 `wave bench`。工具自动创建临时 PostgreSQL，使用本地模拟模型，通过真实 HTTP API 测试鉴权、任务入队、worker、模型流式处理和持久化。它忽略部署的 `WAVE_*` 配置，不需要 API Key；结束或 Ctrl-C 后删除测试容器、数据卷和临时文件。

```bash
make bench
make bench BENCH_ARGS='-workers 2,5,10,20 -clients 32 -tasks 500 -warmup 20 -timeout 5m'
# JSON 报告适合对比不同服务器或提交；构建进度和压测进度写入 stderr。
make -s bench BENCH_ARGS='-format json' > bench.json
```

每档 worker 使用独立数据库，预热不计入结果。报告包含成功任务/秒、失败率、排队/执行/端到端延迟的 P50/P95/P99、数据库连接池等待和进程 Go 堆内存采样峰值。`-model-delay 2s` 可模拟较慢模型，`-chunks`、`-chunk-bytes` 可调整流式响应负载。完整参数见 `wave bench --help` 和 [压测说明](tools/bench/README.zh-CN.md)。

这是一项不含沙箱、浏览器、真实模型和 S3 文件负载的基础压测，不能直接等同于实际 Agent 容量。固定 `-clients`，增加 worker，观察吞吐何时不再明显提升，以及 P95、错误率和资源消耗；较长测试建议至少 500 个任务，并重复运行。`-clients` 是压测端的在途任务数，`-workers` 是服务端 worker 数。

## 客户端与 API

- [CLI](cli/README.zh-CN.md)：`make cli-build`；`wavectl` 操作远程资源，`wave` 管理本机服务。
- SDK：[Go](sdks/go/README.zh-CN.md)、[Python](sdks/python/README.zh-CN.md)、[TypeScript](sdks/typescript/README.zh-CN.md)。
- Agent 技能：[wave-client](skills/wave-client/SKILL.md)、[wave-admin](skills/wave-admin/SKILL.md)。
- [Swagger UI](http://localhost:8080/swagger/index.html) 默认英文，可在下拉框选择中文，或访问 [`?lang=zh-CN`](http://localhost:8080/swagger/index.html?lang=zh-CN)。
- OpenAPI：[英文](api/openapi.json)对应 `/swagger/openapi.json`，[中文](api/openapi.zh-CN.json)对应 `/swagger/openapi.zh-CN.json`。

依次创建 Agent、Session，再提交包含 `agent_id` 和 `input` 的 Task。HTTP 认证使用 `Authorization: Bearer <Wave API Key>`；Swagger Authorize 中填写原始 Key。数据库可用时，`/health` 返回 204。

## 开发

```bash
make build                       # 构建服务。
make test                        # 单元与架构测试。
make check                       # 契约检查、vet 与构建。
make docs clients-generate       # 重新生成 Swagger、双语 OpenAPI 与客户端。
make clients-check clients-test  # 校验客户端生成物与行为。
```

服务代码位于 `cmd/`、`internal/`，协议位于 `api/`，客户端位于 `cli/`、`sdks/`。集成测试、兼容性检查与打包见[工具链说明](tools/clients/README.zh-CN.md)。同步维护英文 `README.md` 和中文 `README.zh-CN.md`；API 注释使用 `中文 || English`。
