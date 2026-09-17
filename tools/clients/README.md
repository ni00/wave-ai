# Client tooling

English | [简体中文](README.zh-CN.md)

Generate Go / Python / TypeScript SDKs, CLI metadata, and command references
from the native OpenAPI 3 contract in `api/openapi.json`.

## Generate and check

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

## Edit generation sources

Edit `api/openapi.json` and the corresponding server DTOs and handlers together.
Keep pagination, transport, idempotency, and CLI command metadata in each
operation's `x-wave-client` extension. Route, schema, and workflow tests check
that the handwritten contract agrees with the service. `config.json` controls
generator versions, package names, and the release version. Do not edit generated SDKs.

Generation preserves handwritten code. Check generated files:

```bash
go run ./tools/clients generate --check
```

Compare with an earlier contract:

```bash
go run ./tools/clients compat BASELINE_SPEC BASELINE_OPERATIONS
```

Replace the placeholders with baseline OpenAPI and operation metadata paths. For a baseline with `x-wave-client`,
pass the baseline OpenAPI path twice.
Run behavior tests as well as compatibility checks.

## Bilingual documentation

Edit Chinese summaries and descriptions in `api/translations.zh-CN.json`, keyed
by JSON Pointer into the English contract. Run `make docs` to update
`api/openapi.zh-CN.json`. Missing or obsolete translations fail generation.
Only documentation text may differ; identifiers, types, examples, and defaults
stay identical. Never translate literal example payloads.

User guides use English `NAME.md` and Chinese
`NAME.zh-CN.md`, with reciprocal language links and matching examples. Keep skill
instructions and generated command references in English.

## Build release packages

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
