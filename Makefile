# Project HANGAR — developer and CI entrypoints.
# Phase 0 exit criterion: `make ci` passes.
#
# ── PROGRESSIVE CI ───────────────────────────────────────────────────────────
# `make ci` must pass at Phase 0, when almost nothing exists yet, AND must
# enforce every invariant by Phase 20. Those two requirements conflict unless
# the checks are phase-aware, so each invariant target SKIPS when its input is
# absent and RUNS when it is present. A check therefore starts enforcing itself
# the moment the phase that introduces it lands — no Makefile edit required.
#
# Set STRICT=1 (CI does, from Phase 15 onward, and always on a release tag) to
# turn every skip into a failure. That is what stops a check silently skipping
# forever because someone deleted its input.
# ─────────────────────────────────────────────────────────────────────────────

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
IMAGE      ?= ghcr.io/hangar-project/hangar
STRICT     ?= 0

# The catalogue snapshot used by the offline identifier-type check. ONE path,
# referenced by the Makefile, the Dockerfile and docs/03 — do not fork it.
SPEC_SNAPSHOT := internal/esi/catalogue/embedded/openapi.snapshot.json

# skip <name> <reason> — honours STRICT.
define skip
	@if [ "$(STRICT)" = "1" ]; then echo "STRICT: $(1) cannot run — $(2)"; exit 1; \
	else echo "skip: $(1) — $(2)"; fi
endef

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z0-9_.-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-24s\033[0m %s\n",$$1,$$2}'

# ── generation ───────────────────────────────────────────────────────────────
.PHONY: generate sqlc openapi types verify-generated
generate: sqlc openapi types ## Run every generator that has inputs

sqlc: ## (Phase 1a) Regenerate internal/store/gen from db/queries
	@if [ -n "$$(ls -A db/queries 2>/dev/null)" ]; then go tool sqlc generate; \
	else $(call skip,sqlc,db/queries is empty); fi

openapi: ## (Phase 15) Emit docs/openapi.json from the Huma router
	@if go run ./cmd/hangar openapi --out docs/openapi.json 2>/dev/null; then echo "openapi.json written"; \
	else $(call skip,openapi,cmd/hangar openapi not implemented yet); fi

types: ## (Phase 16) Emit web/src/api/schema.d.ts from docs/openapi.json
	@if [ -f docs/openapi.json ] && [ -d web/node_modules ]; then cd web && pnpm run api:types; \
	else $(call skip,types,docs/openapi.json or web/node_modules absent); fi

verify-generated: generate ## Principle 10 — generated output must be committed and current
	@if ! git rev-parse --git-dir >/dev/null 2>&1; then $(call skip,verify-generated,not a git repository yet); exit 0; fi; \
	 paths=""; for p in internal/store/gen docs/openapi.json web/src/api/schema.d.ts; do \
	   [ -e "$$p" ] && paths="$$paths $$p"; done; \
	 if [ -z "$$paths" ]; then $(call skip,verify-generated,no generated artefacts exist yet); exit 0; fi; \
	 git diff --exit-code -- $$paths \
	   || { echo "generated files are stale; run 'make generate' and commit"; exit 1; }

# ── database ─────────────────────────────────────────────────────────────────
.PHONY: migrate-up migrate-down
migrate-up:   ## River migrations, then Goose migrations
	go run ./cmd/hangar migrate up
migrate-down: ## Roll back one Goose migration (River migrations are never rolled back automatically)
	go run ./cmd/hangar migrate down

# ── build ────────────────────────────────────────────────────────────────────
.PHONY: web build build-all
web: ## Build the SPA into web/dist for embed.FS
	cd web && pnpm install --frozen-lockfile && pnpm run build

build: web ## Build the single binary for the host platform
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/hangar ./cmd/hangar

build-all: web ## Release matrix (SRS §9.2) — CGO_ENABLED=0 is contractual
	@for t in linux/amd64 linux/arm64 windows/amd64; do \
	  os=$${t%/*}; arch=$${t#*/}; ext=""; [ "$$os" = windows ] && ext=.exe; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" \
	    -o bin/hangar-$$os-$$arch$$ext ./cmd/hangar; \
	done

# ── quality ──────────────────────────────────────────────────────────────────
.PHONY: fmt lint test test-integration bench bench-ledger-clustered web-ci
fmt:  ## gofmt + prettier
	go fmt ./... && cd web && pnpm exec prettier --write .

lint: ## golangci-lint + sqlc vet + eslint (eslint runs with --max-warnings=0)
	golangci-lint run ./...
	@if [ -n "$$(ls -A db/queries 2>/dev/null)" ]; then go tool sqlc vet; \
	else $(call skip,sqlc vet,db/queries is empty); fi
	@if [ -d web/node_modules ]; then cd web && pnpm run lint; \
	else $(call skip,eslint,web/node_modules absent — run 'cd web && pnpm install'); fi

test: ## Go unit tests with race detector
	go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...

test-integration: ## Testcontainers-backed suites, plus the sqlc rules that need a live database
	go test -race -tags=integration -timeout=20m ./...

bench: ## (Phase 4) solo ledger: 1M operations must complete in < 2s
	go test -run=XXX -bench=BenchmarkLedgerSolo -benchtime=1000000x ./internal/esi/ratelimit/...

bench-ledger-clustered: ## (Phase 4) clustered ledger: >= 2000 ops/s/replica at p99 < 10ms
	go test -run=XXX -tags=integration -bench=BenchmarkLedgerClustered ./internal/esi/ratelimit/...

web-ci:
	@if [ -d web/node_modules ]; then cd web && pnpm run ci; \
	else $(call skip,web-ci,web/node_modules absent); fi

# ── invariant gates (see docs/04_RELEASE_GATES.md) ────────────────────────────
# Each guards on the phase artefact that makes it meaningful.
.PHONY: check-money check-identifiers check-alert-sources check-locales check-css check-no-ice

check-money:         ## Principle 9 (Phase 1b) — reflection proof of zero float64 on money paths
	@if [ -d internal/domain ] && [ -n "$$(ls -A internal/domain 2>/dev/null)" ]; then \
	  go test -run TestNoFloatOnMoneyPaths ./internal/domain/...; \
	else $(call skip,check-money,internal/domain is empty); fi

check-identifiers:   ## Principle 13 (Phase 2) — every identifier column matches the ingested spec
	@if [ -f "$(SPEC_SNAPSHOT)" ]; then \
	  go run ./cmd/hangar admin verify-identifier-types --spec "$(SPEC_SNAPSHOT)"; \
	else $(call skip,check-identifiers,$(SPEC_SNAPSHOT) not captured yet); fi

check-alert-sources: ## §4.4 (Phase 14) — every threshold alert's source route is in the sync set
	@if [ -n "$$(ls -A internal/alerting/catalogue 2>/dev/null)" ]; then \
	  go test -run TestThresholdAlertSourceRoutesScheduled ./internal/alerting/...; \
	else $(call skip,check-alert-sources,alert catalogue not seeded yet); fi

check-locales:       ## §4.6 (Phase 3) — all 9 UI locales resolve to a valid ESI Accept-Language
	@if [ -f internal/i18n/locales.json ]; then \
	  go test -run TestLocaleResolutionExhaustive ./internal/i18n/...; \
	else $(call skip,check-locales,internal/i18n/locales.json absent); fi

check-css:           ## §8.1 (Phase 0) — exactly one .css file may exist under web/src
	@n=$$(find web/src -name '*.css' 2>/dev/null | tee /dev/stderr | wc -l | tr -d ' '); \
	 if [ "$$n" = "0" ]; then $(call skip,check-css,web/src has no stylesheet yet); \
	 elif [ "$$n" != "1" ]; then echo "expected exactly 1 stylesheet (web/src/styles/index.css), found $$n"; exit 1; \
	 else echo "check-css: ok"; fi

check-no-ice:        ## §4.3/§9.2 — no ZeroC Ice or cgo dependency may enter the binary
	@if grep -riE 'zeroc|/ice/|glacier2' go.mod go.sum 2>/dev/null; then \
	  echo "ZeroC Ice must not be linked into the binary — it requires cgo and breaks the static builds of SRS §9.2. Use the out-of-process bridge."; exit 1; \
	else echo "check-no-ice: ok"; fi

# ── composite ────────────────────────────────────────────────────────────────
.PHONY: ci ci-strict
ci: verify-generated lint test web-ci check-money check-identifiers check-alert-sources check-locales check-css check-no-ice ## Phase 0 exit criterion; strengthens automatically as phases land

ci-strict: ## Same, but a skipped check is a failure. CI uses this from Phase 15 and on every release tag.
	$(MAKE) ci STRICT=1

.PHONY: docker docker-push compose-up compose-down
docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE):$(VERSION) .
docker-push: docker
	docker push $(IMAGE):$(VERSION)
compose-up:
	docker compose up -d
compose-down:
	docker compose down
