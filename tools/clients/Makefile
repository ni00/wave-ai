# Included by the root Makefile; run targets from the repository root.
CLIENTS_TOOL := go run ./tools/clients
.PHONY: cli-build clients-setup clients-generate clients-check clients-test clients-build clients-integration clients-package clients-smoke
clients-setup:
	npm ci --ignore-scripts --prefix tools/clients
	npm ci --ignore-scripts --prefix sdks/typescript
	uv sync --frozen --project sdks/python
clients-generate:
	$(CLIENTS_TOOL) generate
clients-check:
	node --test tools/clients/localize.test.cjs
	go test -race ./tools/clients ./tests/clients/fixture
	$(CLIENTS_TOOL) check
	$(CLIENTS_TOOL) generate --check
	cd sdks/go && go vet ./...
	cd cli && go vet ./...
clients-test:
	$(CLIENTS_TOOL) test
cli-build:
	cd cli && go build -trimpath -o "$(abspath $(BINDIR))/wavectl" .
clients-build: cli-build
	npm run build --prefix sdks/typescript
clients-integration:
	$(CLIENTS_TOOL) integration
clients-package:
	$(CLIENTS_TOOL) package
clients-smoke:
	$(CLIENTS_TOOL) smoke
