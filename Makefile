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
GO_IMAGE     ?= golang:1.27-alpine
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
FAILING_URL  := https://failing.test
OPEN         := $(if $(filter darwin,$(GOOS)),open,xdg-open)

.PHONY: build build-all test vet tidy check-links skill-refs \
	install up down status doctor errors uninstall \
	demo demo-down e2e e2e-inspect e2e-transcript e2e-incident e2e-down clean

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

## skill-refs: regenerate the contracts the agent skill quotes from docs/
# The Skill's references embed the label table and the two checklists verbatim;
# `go test ./...` fails when they drift, and this rewrites them. See
# docs/adr/0005-the-agent-skill-ships-in-the-cli.md.
skill-refs:
	docker run $(DOCKER_FLAGS) $(GO_IMAGE) \
		go test ./internal/skill -run TestGeneratedBlocksMatchDocs -update

## check-links: validate Markdown links + anchors (lychee, offline — same as CI)
check-links:
	docker run --rm -w /input -v "$(CURDIR)":/input $(LYCHEE_IMAGE) \
		--offline --include-fragments --no-progress README.md CLAUDE.md CONTEXT.md docs

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

## doctor: report every check on this host, with a remedy per failure
doctor: build
	$(BIN) doctor

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
# The agent's URL carries a digest of its content, so nothing here hardcodes it:
# the path is read back out of the page, which is also what proves the tag was
# injected in the first place.
e2e-inspect: build
	docker compose -f $(DEMO_COMPOSE) up -d
	@echo "==> waiting for $(INSPECT_URL) to serve the injected agent"
	@for i in $$(seq 1 30); do \
		curl -fsS $(INSPECT_URL) 2>/dev/null | grep -q data-proximo-exchange && break || sleep 1; \
	done
	@agent=$$(curl -fsS $(INSPECT_URL) | sed -n 's|.*<script src="\(/\.proximo/agent\.[^"]*\)".*|\1|p' | head -1); \
	if [ -z "$$agent" ]; then echo "FAIL: agent not injected into $(INSPECT_URL)"; exit 1; fi; \
	echo "    injected: $$agent"; \
	curl -fsS "$(INSPECT_URL)$$agent" | grep -q "proximo Inspection agent" \
		|| { echo "FAIL: $$agent not served on the reserved path"; exit 1; }
	@curl -fsS $(DEMO_URL) | grep -q data-proximo-exchange \
		&& { echo "FAIL: $(DEMO_URL) carries no label but was injected into"; exit 1; } || true
	@$(BIN) errors --json | grep -q "inspected.test" \
		|| { echo "FAIL: no Exchange recorded for inspected.test"; exit 1; }
	@echo "OK: agent injected and served, unlabelled route untouched, Exchange recorded"
	@echo "    (client reports need a browser: open $(INSPECT_URL) and run setTimeout(function(){ null.foo }, 0))"
	$(BIN) errors --host inspected.test

## e2e-transcript: prove the Transcript end to end (stack must be installed/up)
# The two things unit tests cannot reach: Traefik's real access-log output, and
# Docker's real multiplexed log stream. Neither is mocked here — a wrong decoder
# yields no Exchange at all, and a wrong demultiplexer yields a Transcript of
# frame headers instead of the line nginx wrote.
#
# `failing` carries no proximo.inspect, which is the point: a route needs no
# label and no browser to be diagnosable.
e2e-transcript: build
	docker compose -f $(DEMO_COMPOSE) up -d
	@echo "==> waiting for $(FAILING_URL) to route"
	@for i in $$(seq 1 30); do \
		[ "$$(curl -sS -o /dev/null -w '%{http_code}' $(FAILING_URL)/ 2>/dev/null)" = "200" ] && break || sleep 1; \
	done
	@code=$$(curl -sS -o /dev/null -w '%{http_code}' $(FAILING_URL)/boom); \
	[ "$$code" = "500" ] || { echo "FAIL: $(FAILING_URL)/boom answered $$code, want 500"; exit 1; }
	@echo "==> the Access record came from Traefik's log, with no label on the container"
	@$(BIN) errors --json --host failing.test | grep -q '"status": 500' \
		|| { echo "FAIL: no failing Exchange for failing.test — Traefik's access log was not read"; exit 1; }
	@echo "==> the Transcript quotes what the container wrote"
	@$(BIN) errors --json --host failing.test | grep -q '/boom' \
		|| { echo "FAIL: the Transcript does not quote nginx's own line for /boom"; exit 1; }
	@id=$$($(BIN) errors --json --host failing.test | sed -n 's/.*"id": "\([0-9a-f]*\)".*/\1/p' | head -1); \
	[ -n "$$id" ] && echo "    Exchange $$id"; \
	$(BIN) errors transcript "$$id" | grep -q '/boom' \
		|| { echo "FAIL: \`errors transcript\` printed no transcript for $$id"; exit 1; }
	@echo "OK: every route produces an Exchange, and its Transcript is quoted back"
	$(BIN) errors --host failing.test

## e2e-incident: prove Incidents end to end (stack must be installed/up)
# The one thing unit tests cannot reach: Docker's real event stream. The `worker`
# service has no host and no route — it is known to proximo only because it
# carries proximo.transcript — so a missing Incident here means the watcher never
# saw the container die, and a missing Transcript means the window the Incident
# fixed did not reach the container's own output.
e2e-incident: build
	docker compose -f $(DEMO_COMPOSE) up -d
	@echo "==> waiting for the worker to exit and be restarted"
	@for i in $$(seq 1 30); do \
		$(BIN) errors --json --service worker 2>/dev/null | grep -q '"kind": "exited"' && break || sleep 2; \
	done
	@$(BIN) errors --json --service worker | grep -q '"exit_code": 1' \
		|| { echo "FAIL: no Incident for the worker - the watcher did not record the exit"; exit 1; }
	# 'nil map' is the worker's own panic line in $(DEMO_COMPOSE): if the window
	# quotes it, the Incident reached the container's output.
	@echo "==> the Incident's window quotes what the worker wrote"
	@$(BIN) errors --json --service worker | grep -q 'nil map' \
		|| { echo "FAIL: the Incident's Transcript does not quote the worker's own line"; exit 1; }
	@id=$$($(BIN) errors --json --service worker | sed -n 's/.*"id": "\([0-9a-f]*\)".*/\1/p' | head -1); \
	if [ -z "$$id" ]; then echo "FAIL: no Incident id in the listing"; exit 1; fi; \
	echo "    Incident $$id"; \
	$(BIN) errors transcript "$$id" | grep -q 'nil map' \
		|| { echo "FAIL: \`errors transcript\` printed no transcript for Incident $$id"; exit 1; }
	@echo "==> a worker that stops advancing loses a healthcheck that was passing, and that is an Incident"
	@for i in $$(seq 1 30); do \
		$(BIN) errors --json --service stalling 2>/dev/null | grep -q '"kind": "unhealthy"' && break || sleep 2; \
	done
	@$(BIN) errors --json --service stalling | grep -q '"kind": "unhealthy"' \
		|| { echo "FAIL: the stalled worker produced no unhealthy Incident - a healthcheck is how a stuck container becomes visible"; exit 1; }
	# Nothing is held back: the Incident is a check that was passing and stopped,
	# so the boot-time noise the old exclusion existed for never reaches the store.
	@$(BIN) errors --json | grep -q '"kind": "unhealthy"' \
		|| { echo "FAIL: the unhealthy Incident must be in the default listing too"; exit 1; }
	# The readings are taken on every --service, Incident or no Incident, and a
	# scaled service produces one per running container.
	@echo "==> --service ends with the readings"
	@$(BIN) errors --json --service stalling | grep -q '"readings"' \
		|| { echo "FAIL: a --service listing carries no readings"; exit 1; }
	@$(BIN) errors --service stalling | grep -q "What proximo can see of stalling right now" \
		|| { echo "FAIL: the readings did not print after the listing"; exit 1; }
	@echo "OK: a container with no route produced an Incident, and its window quoted the container's output"
	$(BIN) errors --service worker

## e2e-down: stop demo and uninstall (restore the host)
e2e-down: build
	-docker compose -f $(DEMO_COMPOSE) down
	$(BIN) uninstall

## clean: remove build artifacts
clean:
	rm -rf bin dist
