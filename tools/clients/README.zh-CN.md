# 客户端工具链

中文 | [English](README.md)

服务端 Go 类型和 Swagger 注释是契约来源。工具链将 Swagger 2 转成 OpenAPI 3，生成 Go、Python、TypeScript API，以及 CLI 元数据和命令参考；SSE、重试、文件流、任务等待等行为由各语言的手写运行时提供。

## 目录与所有权

| 目录或文件 | 用途 |
| --- | --- |
| `internal/platform/apidocs/` | `make docs` 生成的 Swagger 2，勿手改 |
| `api/openapi.zh-CN.json`、`api/openapi.json` | 中文与英文 OpenAPI 3 契约 |
| `tools/clients/` | 生成、兼容性检查、发布配置和工具依赖 |
| `tests/clients/` | 共享 HTTP 夹具、确定性模型与真实服务测试 |
| `sdks/go/generated/`、`sdks/python/wave_ai_generated/`、`sdks/typescript/src/generated/` | 自动生成的 API 与类型 |
| `cli/internal/command/` | CLI 用户流程、配置和终端输出 |
| `skills/` | 外部 Agent 操作说明、工作流参考和请求样例 |

具体生成路径以 `generate.go` 的输出清单为准。`config.json` 固定生成器镜像摘要和包名；`operations.json` 记录 OpenAPI 无法完整表达的分页、传输、幂等性与命令命名。新增 operationId 必须补齐这些元数据，检查器会拒绝漏项。

## 日常开发

Go 统一执行工具链，Node.js 负责协议转换，Docker 运行固定版本的生成器。Python/uv 仅用于 Python SDK 的测试和打包。在仓库根目录执行：

```bash
make clients-setup       # 按锁文件安装工具、TS 与 Python 依赖
make docs                # 接口变更时先更新 Swagger
make clients-generate    # 转换、校验并重新生成三种 SDK 与 CLI 元数据
make clients-check       # 契约/技能校验、生成一致性、Go vet
make clients-test        # 共享 HTTP 夹具 + SDK/CLI 回归测试
make clients-integration # 临时 PostgreSQL、S3、确定性模型下的完整工作流
```

生成器在临时目录完成生成后更新清单内文件，保留手写运行时。`--check` 重新生成后比较结果，避免提交过期产物。CLI 单独开发可用 `make cli-build`，无需安装其他语言依赖。

统一入口是 `go run ./tools/clients <command>`，支持 `generate [--check]`、`check`、`compat SPEC OPERATIONS`、`test`、`integration`、`package`、`smoke`。Make 目标调用同一工具；Go 源文件按职责拆分在同一个包中，不再保留独立的 Python 编排脚本。

`tests/clients/fixture/` 提供认证、分页、错误、SSE、重试和文件的共享 HTTP 用例。CLI 测试另覆盖校验、配置、脱敏与退出码。真实服务测试串起任务审批、工具结果、完成和文件传输，也执行 Skill 工作流。Python SDK 测试归于 `sdks/python/tests/`，生成的 Python API 保留在 `wave_ai_generated/`。测试会清理自己创建的容器和数据，中断时也执行清理。

兼容性检查由 `.github/workflows/clients.yml` 在 PR 中对比基线契约和操作元数据。它检查协议变化；新增行为仍需要运行时测试，不能只看代码生成成功。

## 双语文档

在源码中成对维护中英文，每行用 `中文 || English` 分隔：

```go
// @Summary 创建任务 || Create a task
// @Param id path string true "会话 ID || Session ID"
// @Failure 404 {object} apierr.Envelope "任务不存在 || Task not found"
```

`@Description`、响应头和字段注释使用相同约定。`localize.cjs` 只选择摘要和说明的语言，保留标识符、类型、示例、枚举和默认值。`Agent ID` 等通用技术文本无需重复。缺失英文会使生成失败。旧 Swagger 2 保留成对注释；OpenAPI 3 分别发布两种语言。

修改后运行 `make docs clients-generate`。英文协议是 SDK/CLI 的默认生成输入，`api/openapi.zh-CN.json` 提供等价中文协议。校验确保两版只在说明文字上不同。服务嵌入和发布归档都包含两版；Swagger UI 默认英文，通过 `?lang=zh-CN` 选择中文，页面下拉框和 API 说明中的链接都可以切换。

各组件 README 使用中文 `README.zh-CN.md` 与英文 `README.md`，修改时同步两版，保持示例和链接对应。

## 本地发布包

```bash
make clients-package
make clients-smoke
```

输出位于 `dist/clients/<version>/`：六个平台 CLI、Python wheel/sdist、npm tarball、Go 源码包、两份 Skill ZIP、中英文 OpenAPI、manifest 和 SHA256SUMS。全部构建成功后才替换产物目录；冒烟测试校验摘要，并在新临时目录安装实际归档。以上命令不发布包、不创建 Git tag。

版本和目标包名统一配置在 `config.json`，发布前同步各模块版本并运行检查。仓库地址为 `https://github.com/ni00/wave-ai`，Go 子模块需要 `sdks/go/v<version>` 和 `cli/v<version>` 标签；Python/npm 包名与 registry 权限需在正式发布前确认。当前 `cli/go.work` 仅用于仓库内联调，发布的 CLI 依赖已发布的同版本 Go SDK。

Skill ZIP 根目录包含 `SKILL.md`，同时打包 `references/`、`assets/` 和 `agents/`。`wave-client/references/commands.md` 随契约生成，其他工作流内容手动维护。Skill 的使用说明保存在各自目录，不依赖额外的根级文档目录。
