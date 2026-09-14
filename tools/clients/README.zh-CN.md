# 客户端工具链

[English](README.md) | 简体中文

从服务端类型和注释生成 OpenAPI 契约、Go / Python / TypeScript SDK、CLI 元数据及命令参考。

## 1. 生成与检查

需要 Go 1.27+、Node.js 22+、Docker，以及用于 Python SDK 的 Python 3.10+ 和 uv。在仓库根目录执行：

```bash
make clients-setup               # 安装依赖。
make docs clients-generate       # 生成契约与客户端。
make clients-check clients-test  # 检查一致性并运行测试。
```

API 或工作流行为变化后，运行 `make clients-integration`，验证任务审批、工具结果和文件传输等完整流程。只修改 README 时，运行 `go run ./tools/clients check`。

## 2. 修改生成来源

修改服务端类型和 Swagger 注释后重新生成，不直接编辑生成结果。新增 `operationId` 时，须在 `operations.json` 补充分页、传输、幂等性和命令命名等元数据。生成器版本、包名及发布版本由 `config.json` 管理。

生成会保留手写代码。检查生成结果是否同步，可运行：

```bash
go run ./tools/clients generate --check
```

与旧版本比较兼容性：

```bash
go run ./tools/clients compat BASELINE_SPEC BASELINE_OPERATIONS
```

将两个占位符替换为基线 OpenAPI 和操作元数据文件路径。兼容性检查不能替代行为测试。

## 3. 双语文档

可翻译的 API 注释使用 `中文 || English`，适用于摘要、描述、参数、响应头和字段说明：

```go
// @Summary 创建任务 || Create a task
// @Param id path string true "会话 ID || Session ID"
// @Failure 404 {object} apierr.Envelope "任务不存在 || Task not found"
```

`Agent ID` 等通用文本无需重复；缺少必需的英文翻译会使生成失败。

`api/openapi.json` 是 SDK 和 CLI 的生成输入，`api/openapi.zh-CN.json` 是中文版本。两版只允许说明文字不同，标识符、类型、示例和默认值保持一致。README 同步维护中英文，示例和链接保持对应。

## 4. 构建发布包

```bash
make clients-package
make clients-smoke
```

产物位于 `dist/clients/<version>/`，包括六个平台的 CLI、三种 SDK、两份技能包、中英文 OpenAPI，以及 manifest 和 `SHA256SUMS`。冒烟测试校验摘要并安装归档包；上述命令不上传包或创建 Git 标签。

发布前同步 `config.json` 与各模块版本，并重新运行检查。Go 子模块使用 `sdks/go/v<version>` 和 `cli/v<version>` 标签；发布的 CLI 依赖同版本的已发布 Go SDK。
