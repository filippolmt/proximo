BINARY := proximo
PKG := github.com/filippolmt/proximo
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Host target. The binary is named per OS/arch so a macOS build and a Linux
# build never overwrite each other in a shared working tree.
GOOS    ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
RAWARCH := $(shell uname -m)
GOARCH  ?= $(patsubst aarch64,arm64,$(patsubst x86_64,amd64,$(RAWARCH)))
BIN     := bin/$(BINARY)-$(GOOS)-$(GOARCH)
SRC     := $(CURDIR)

# All Go work runs through Docker — no local Go toolchain required.
GO_IMAGE     ?= golang:1.26-alpine
LYCHEE_IMAGE ?= lycheeverse/lychee:latest
DOCKER_FLAGS := --rm -v "$(CURDIR)":/src -w /src \
	-v proximo-go-mod:/go/pkg/mod -v proximo-go-build:/root/.cache/go-build \
	-e CGO_ENABLED=0

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

DEMO_COMPOSE := examples/whoami/docker-compose.yml
DEMO_URL     := https://whoami.test
OPEN         := $(if $(filter darwin,$(GOOS)),open,xdg-open)

.PHONY: build build-all test vet tidy check-links \
	install up down status uninstall \
	demo demo-down e2e e2e-down clean

# ---- Build (Go runs in Docker) ------------------------------------------------

## build: compile ./bin/proximo-<os>-<arch> for the host (override GOOS/GOARCH)
build:
	docker run $(DOCKER_FLAGS) -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) $(GO_IMAGE) \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

## build-all: cross-compile all release targets (darwin,linux × amd64,arm64)
build-all:
	$(MAKE) build GOOS=darwin GOARCH=arm64
	$(MAKE) build GOOS=darwin GOARCH=amd64
	$(MAKE) build GOOS=linux  GOARCH=arm64
	$(MAKE) build GOOS=linux  GOARCH=amd64

# ---- Tests / checks (always Linux, in the golang image) -----------------------

## test: run the test suite in Docker (no local Go needed)
test:
	docker run $(DOCKER_FLAGS) $(GO_IMAGE) go test ./...

## vet: go vet in Docker
vet:
	docker run $(DOCKER_FLAGS) $(GO_IMAGE) go vet ./...

## tidy: go mod tidy in Docker
tidy:
	docker run $(DOCKER_FLAGS) $(GO_IMAGE) go mod tidy

## check-links: validate Markdown links + anchors (lychee, offline — same as CI)
check-links:
	docker run --rm -w /input -v "$(CURDIR)":/input $(LYCHEE_IMAGE) \
		--offline --include-fragments --no-progress README.md CLAUDE.md docs

# ---- Lifecycle (host binary; build first so the arch always matches) ----------

## install: host setup (CA, resolver, trust) + start stack (asks for sudo)
install: build
	PROXIMO_SRC="$(SRC)" $(BIN) install

## up: start the stack (builds images from local source)
up: build
	PROXIMO_SRC="$(SRC)" $(BIN) up

## down: stop the stack
down: build
	$(BIN) down

## status: list routed containers
status: build
	$(BIN) status

## uninstall: reverse host changes and tear down the stack
uninstall: build
	$(BIN) uninstall

# ---- Demo / end-to-end --------------------------------------------------------

## demo: start the whoami sample and open it (stack must be installed/up)
demo:
	docker compose -f $(DEMO_COMPOSE) up -d
	@echo "Routed at $(DEMO_URL)"
	-$(OPEN) $(DEMO_URL)

## demo-down: stop the whoami sample
demo-down:
	docker compose -f $(DEMO_COMPOSE) down

## e2e: install + start demo + open browser
e2e: install
	docker compose -f $(DEMO_COMPOSE) up -d
	$(BIN) status
	-$(OPEN) $(DEMO_URL)

## e2e-down: stop demo and uninstall (restore the host)
e2e-down: build
	-docker compose -f $(DEMO_COMPOSE) down
	$(BIN) uninstall

## clean: remove build artifacts
clean:
	rm -rf bin dist
