# Included by the root Makefile; run targets from the repository root.
.PHONY: cli-build clients-setup clients-generate clients-check clients-test clients-build clients-integration clients-package clients-smoke
clients-setup:
	npm ci --ignore-scripts --prefix tools/clients
	npm ci --ignore-scripts --prefix sdks/typescript
	uv sync --frozen --project sdks/python
clients-generate:
	python3 tools/clients/generate.py
clients-check:
	node --test tools/clients/localize.test.cjs
	python3 tools/clients/check.py
	python3 tools/clients/generate.py --check
	cd sdks/go && go vet ./...
	cd cli && go vet ./...
clients-test:
	python3 tests/clients/run.py
cli-build:
	cd cli && go build -trimpath -o "$(abspath $(BINDIR))/wavectl" .
clients-build: cli-build
	npm run build --prefix sdks/typescript
clients-integration:
	npm run build --prefix sdks/typescript
	python3 tests/clients/integration.py
clients-package:
	python3 tools/clients/release.py
clients-smoke:
	python3 tools/clients/smoke.py
