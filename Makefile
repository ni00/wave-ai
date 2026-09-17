CACHE_DIR := $(if $(XDG_CACHE_HOME),$(XDG_CACHE_HOME),$(HOME)/.cache)/wave-ai
BINDIR ?= $(CACHE_DIR)/bin
COMPOSE := docker compose --project-directory deploy
DOCS_DIR ?= api

.PHONY: build run test integration check docs docs-check setup up down bench bench-test bench-sandbox
build: web-build
	go build -trimpath -tags=nomsgpack -o "$(BINDIR)/wave" ./cmd/wave
run: build
	"$(BINDIR)/wave" serve
test: web-build
	go test ./...
bench: build
	"$(BINDIR)/wave" bench $(BENCH_ARGS)
bench-sandbox: build
	"$(BINDIR)/wave" bench sandbox $(BENCH_ARGS)
bench-test: web-build
	WAVE_BENCH_INTEGRATION=1 go test -race ./tools/bench -count=1
integration: web-build
	bash internal/tests/integration.sh
check: docs-check build
	go vet ./...
	go mod verify
docs:
	node tools/clients/localize.cjs api/openapi.json api/translations.zh-CN.json "$(DOCS_DIR)/openapi.zh-CN.json"
docs-check: web-build
	bash internal/tests/docs.sh
	go test ./internal/app -run 'TestOpenAPI|TestRoutingErrors' -count=1
setup:
	sh deploy/setup.sh
up: setup
	$(COMPOSE) up --build -d
down:
	$(COMPOSE) down

include tools/clients/Makefile

.PHONY: web-build web-test web-dev
web-build:
	npm ci --ignore-scripts --prefix web >&2
	npm run build --prefix web >&2
web-test:
	npm test --prefix web
web-dev:
	npm run dev --prefix web -- --port 5173
