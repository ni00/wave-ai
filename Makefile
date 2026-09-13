CACHE_DIR := $(if $(XDG_CACHE_HOME),$(XDG_CACHE_HOME),$(HOME)/.cache)/wave-ai
BINDIR ?= $(CACHE_DIR)/bin
COMPOSE := docker compose --project-directory deploy
DOCS_DIR ?= internal/platform/apidocs

.PHONY: build run test integration check docs docs-check setup up down
build:
	go build -trimpath -tags=nomsgpack -o "$(BINDIR)/wave" ./cmd/wave
run: build
	"$(BINDIR)/wave" serve
test:
	go test ./...
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
