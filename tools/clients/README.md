# Client tooling

English | [简体中文](README.zh-CN.md)

Generate OpenAPI contracts, Go / Python / TypeScript SDKs, CLI metadata, and
command references from server types and annotations.

## 1. Generate and check

Requires Go 1.27+, Node.js 22+, Docker, and Python 3.10+ with uv for the Python
SDK. From the repository root:

```bash
make clients-setup               # Install dependencies.
make docs clients-generate       # Generate contracts and clients.
make clients-check clients-test  # Check freshness and run tests.
```

After API or workflow behavior changes, run `make clients-integration` to verify
workflows such as approval, tool results, and file transfers. For README-only
changes, run `go run ./tools/clients check`.

## 2. Edit generation sources

Edit server types and Swagger annotations, then regenerate. Do not edit generated
output directly. Add metadata to `operations.json` for every new `operationId`,
including pagination, transport, idempotency, and command naming. `config.json`
controls generator versions, package names, and the release version.

Generation preserves handwritten code. To check whether generated files are current:

```bash
go run ./tools/clients generate --check
```

To compare compatibility with an earlier version:

```bash
go run ./tools/clients compat BASELINE_SPEC BASELINE_OPERATIONS
```

Replace the placeholders with the baseline OpenAPI and operation metadata paths.
Compatibility checks do not replace behavior tests.

## 3. Bilingual documentation

Use `中文 || English` for translatable summaries, descriptions, parameters,
response headers, and field comments:

```go
// @Summary 创建任务 || Create a task
// @Param id path string true "会话 ID || Session ID"
// @Failure 404 {object} apierr.Envelope "任务不存在 || Task not found"
```

Shared terms such as `Agent ID` need no pair. Missing required English
translations fail generation.

`api/openapi.json` is the SDK/CLI generation input; `api/openapi.zh-CN.json` is the
Chinese version. Only documentation text may differ; identifiers, types, examples,
and defaults stay identical. Update both README languages together, with matching
examples and links.

## 4. Build release packages

```bash
make clients-package
make clients-smoke
```

Artifacts in `dist/clients/<version>/` include CLI archives for six platforms,
three SDKs, two skill packages, both OpenAPI contracts, a manifest, and
`SHA256SUMS`. Smoke tests verify checksums and install archives. These commands do
not publish packages or create Git tags.

Before publishing, synchronize `config.json` and module versions, then rerun
checks. Go submodules use `sdks/go/v<version>` and `cli/v<version>` tags. The
released CLI depends on the published Go SDK at the same version.
