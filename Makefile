CACHE_DIR := $(if $(XDG_CACHE_HOME),$(XDG_CACHE_HOME),$(HOME)/.cache)/wave-ai
BINDIR ?= $(CACHE_DIR)/bin
COMPOSE := docker compose --project-directory deploy
DOCS_DIR ?= internal/platform/apidocs

.PHONY: build run test integration check docs docs-check setup up down bench bench-test bench-sandbox
build:
	go build -trimpath -tags=nomsgpack -o "$(BINDIR)/wave" ./cmd/wave
run: build
	"$(BINDIR)/wave" serve
test:
	go test ./...
bench: build
	"$(BINDIR)/wave" bench $(BENCH_ARGS)
bench-sandbox: build
	"$(BINDIR)/wave" bench sandbox $(BENCH_ARGS)
bench-test:
	WAVE_BENCH_INTEGRATION=1 go test -race ./tools/bench -count=1
integration:
	bash internal/tests/integration.sh
check:
	$(MAKE) docs-check
	go vet ./...
	go mod verify
	$(MAKE) build
docs:
	go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g app/app.go -d internal -o "$(DOCS_DIR)" --packageName apidocs --parseInternal
docs-check:
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
	npm ci --ignore-scripts --prefix web
	npm run build --prefix web
web-test:
	npm test --prefix web
web-dev:
	npm run dev --prefix web -- --port 5173
