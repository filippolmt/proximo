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
INSPECT_URL  := https://inspected.test
OPEN         := $(if $(filter darwin,$(GOOS)),open,xdg-open)

.PHONY: build build-all test vet tidy check-links vendor-agent \
	install up down status errors uninstall \
	demo demo-down e2e e2e-inspect e2e-down capture-envelope clean

# ---- Vendored browser agent ---------------------------------------------------
# The injected agent is @sentry/browser. Its npm package ships only ESM/CJS, so
# the browser bundle is built here with esbuild (both versions pinned) and the
# result is committed: building proximo — and building the stack images from the
# published module — must need nothing but the module itself, and an inspected
# page must work with no network. The entry re-exports only `init`, so
# tree-shaking drops tracing, replay, feedback and the AI integrations: ~89 KB
# raw, ~30 KB gzipped — and the hop serves it gzipped, content-addressed and
# immutable, so a page pays for it once per proximo version.
SENTRY_VERSION  ?= 10.72.0
ESBUILD_VERSION ?= 0.25.10
NODE_IMAGE      ?= node:22-alpine
PUPPETEER_IMAGE ?= ghcr.io/puppeteer/puppeteer:latest
AGENT_SDK       := internal/inspect/assets/sentry.min.js

$(AGENT_SDK):
	docker run --rm -v "$(CURDIR)/$(@D)":/out $(NODE_IMAGE) sh -c '\
		set -e; cd /tmp; \
		npm install --silent --no-audit --no-fund \
			@sentry/browser@$(SENTRY_VERSION) esbuild@$(ESBUILD_VERSION) >/dev/null 2>&1; \
		echo "export { init, captureMessage } from \"@sentry/browser\";" > entry.js; \
		./node_modules/.bin/esbuild entry.js --bundle --format=iife --global-name=Sentry \
			--minify --target=es2018 --log-level=error \
			--banner:js="/* @sentry/browser $(SENTRY_VERSION), bundled by esbuild $(ESBUILD_VERSION) via \`make vendor-agent\`. Do not edit. */" \
			--outfile=/out/$(@F)'

## vendor-agent: rebuild the pinned @sentry/browser bundle the hop injects
vendor-agent:
	rm -f $(AGENT_SDK)
	$(MAKE) $(AGENT_SDK)

## capture-envelope: re-record the parser's fixture from the vendored agent
# The envelope parser reads a format proximo does not own, so it is tested
# against bytes the SDK really produced: this drives the vendored agent in a real
# browser and saves the envelope it sends. Run it after every vendor-agent — a
# test fails until the fixture and the bundle name the same version.
capture-envelope: $(AGENT_SDK)
	docker run --rm -v "$(CURDIR)":/src -w /src -e NODE_PATH=/home/pptruser/node_modules \
		$(PUPPETEER_IMAGE) node internal/inspect/testdata/capture.js

# ---- Build (Go runs in Docker) ------------------------------------------------

## build: compile ./bin/proximo-<os>-<arch> for the host (override GOOS/GOARCH)
build: $(AGENT_SDK)
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

## errors: show Exchanges from inspected routes (ARGS="--host web.test --json")
errors: build
	@$(BIN) errors $(ARGS)

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

## e2e-inspect: prove Inspection end to end (stack must be installed/up)
e2e-inspect: build
	docker compose -f $(DEMO_COMPOSE) up -d
	@echo "==> waiting for $(INSPECT_URL) to serve the injected agent"
	@for i in $$(seq 1 30); do \
		curl -fsS $(INSPECT_URL) 2>/dev/null | grep -q "/.proximo/agent.js" && break || sleep 1; \
	done
	@curl -fsS $(INSPECT_URL) | grep -q "/.proximo/agent.js" \
		|| { echo "FAIL: agent not injected into $(INSPECT_URL)"; exit 1; }
	@curl -fsS $(INSPECT_URL)/.proximo/agent.js | grep -q "proximo" \
		|| { echo "FAIL: agent.js not served on the reserved path"; exit 1; }
	@curl -fsS $(DEMO_URL) | grep -q "/.proximo/agent.js" \
		&& { echo "FAIL: an unlabelled route was injected into"; exit 1; } || true
	@$(BIN) errors --json | grep -q "inspected.test" \
		|| { echo "FAIL: no Exchange recorded for inspected.test"; exit 1; }
	@echo "OK: agent injected and served, unlabelled route untouched, Exchange recorded"
	$(BIN) errors --host inspected.test

## e2e-down: stop demo and uninstall (restore the host)
e2e-down: build
	-docker compose -f $(DEMO_COMPOSE) down
	$(BIN) uninstall

## clean: remove build artifacts
clean:
	rm -rf bin dist
