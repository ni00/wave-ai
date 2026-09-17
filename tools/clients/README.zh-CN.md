# 客户端工具链

[English](README.md) | 简体中文

从 `api/openapi.json` 原生 OpenAPI 3 契约生成 Go / Python / TypeScript SDK、CLI 元数据及命令参考。

## 生成与检查

需要 Go 1.27+、Node.js 22+、Docker，以及用于 Python SDK 的 Python 3.10+ 和 uv。在仓库根目录执行：

```bash
make clients-setup               # 安装依赖。
make docs clients-generate       # 生成契约与客户端。
make clients-check clients-test  # 检查一致性并运行测试。
```

API 或工作流行为变化后，运行 `make clients-integration`，验证任务审批、工具结果和文件传输等完整流程。只修改 README 时，运行 `go run ./tools/clients check`。

## 修改生成来源

同步修改 `api/openapi.json` 与服务端 DTO、处理器。每个操作的 `x-wave-client` 扩展保存分页、传输、幂等性和 CLI 命令元数据。路由、字段和工作流测试检查手写契约与服务的行为是否一致。生成器版本、包名及发布版本由 `config.json` 管理。不要直接修改生成的 SDK。

生成保留手写代码。检查生成文件：

```bash
go run ./tools/clients generate --check
```

与旧契约比较：

```bash
go run ./tools/clients compat BASELINE_SPEC BASELINE_OPERATIONS
```

将占位符替换为基线 OpenAPI 和操作元数据路径。基线包含 `x-wave-client` 时，两个参数均传入基线 OpenAPI 路径。除兼容性检查外，还需运行行为测试。

## 双语文档

在 `api/translations.zh-CN.json` 中修改中文摘要和说明，以英文契约中的 JSON Pointer 为键。运行 `make docs` 更新 `api/openapi.zh-CN.json`。缺失或过时的翻译会使生成失败。两版只允许说明文字不同，标识符、类型、示例和默认值保持一致。不要翻译示例中的字面数据。

用户指南分别使用英文 `NAME.md` 和中文 `NAME.zh-CN.md`，提供语言切换链接，示例保持对应。技能指令和生成的命令参考保留英文。

## 构建发布包

```bash
make clients-package
make clients-smoke
```

产物位于 `dist/clients/<version>/`，包括六个平台的 CLI、三种 SDK、两份技能包、中英文 OpenAPI，以及 manifest 和 `SHA256SUMS`。冒烟测试校验摘要并安装归档包；上述命令不上传包或创建 Git 标签。

发布前同步 `config.json` 与各模块版本，并重新运行检查。Go 子模块使用 `sdks/go/v<version>` 和 `cli/v<version>` 标签；发布的 CLI 依赖同版本的已发布 Go SDK。
