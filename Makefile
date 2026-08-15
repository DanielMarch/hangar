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
#
# PHASE 14.1 FIX. This was a `define ... endef` block whose body began with a
# TAB and an `@` and spanned two lines. Every call site uses it INLINE inside
# an if/else recipe (`... else $(call skip,x,y); fi`), so that expansion
# injected a newline, a tab and an `@` into the middle of a shell compound
# command — and the recipe died with "syntax error near unexpected token
# `then'" before it could evaluate anything. It affected EVERY guarded gate:
# check-money, check-identifiers, check-alert-sources, check-locales,
# check-css, sqlc, openapi, types, verify-generated — i.e. `make ci` as a
# whole, which is Phase 0's own exit criterion. Not a Windows quirk; the
# expansion is equally broken under GNU Make on Linux.
#
# A recursively-expanded single-line variable is the fix: no embedded newline,
# no tab, no `@` (the call sites already prefix their own). It behaves
# identically in both positions — standalone or inside an else branch.
skip = if [ "$(STRICT)" = "1" ]; then echo "STRICT: $(1) cannot run — $(2)"; exit 1; else echo "skip: $(1) — $(2)"; fi

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z0-9_.-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-24s\033[0m %s\n",$$1,$$2}'

# ── generation ───────────────────────────────────────────────────────────────
# TWO SEPARATE HAZARDS BIT PHASE 15/15.1 HERE. Both are recorded because
# each cost real debugging time and neither is obvious from the recipes.
#
# 1. CONCURRENCY (the sqlc "user-mapped section" error). Do not run
#    `generate` or `verify-generated` in the background while another one —
#    or a foreground `sqlc generate` — is also running. Two processes
#    rewriting internal/store/gen/*.go at once fails on Windows with
#      The requested operation cannot be performed on a file with a
#      user-mapped section open.
#    because the loser hits a file the winner (or a real-time virus scan of
#    it) currently has memory-mapped. Phase 15 hit this repeatedly and
#    worked around it by regenerating into a temp directory. It does not
#    reproduce serially: 34 controlled attempts (serial, post-build,
#    post-vet, post-lint, and concurrent) all passed.
#
# 2. `git diff` HANGING (Phase 15.1). verify-generated's staleness check
#    used `git diff --exit-code -- <paths>`, and that command hung
#    indefinitely inside a long-running `make` — twice, ~13+ minutes with
#    the make processes at near-zero CPU, blocking the whole gate. It does
#    NOT reproduce standalone (no core.pager, no fsmonitor, no stale
#    index.lock, 14 MiB repo, diff only ~500 lines), so the mechanism is
#    NOT proven — do not read the fix below as a diagnosis.
#
#    What the fix does is remove the failure surface rather than explain
#    it: `--quiet` means git writes NO output at all (the check only ever
#    wanted the exit code), and `--no-pager` means no pager can be spawned
#    under any tty-detection outcome. On failure a bounded `--name-only`
#    list is printed instead of a potentially enormous diff, which is also
#    better CI output. A gate that can hang forever is worse than one that
#    fails, so if this ever recurs, add a timeout rather than restoring the
#    old form.
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
	 if ! git --no-pager diff --quiet -- $$paths; then \
	   echo "generated files are stale; run 'make generate' and commit. Changed:"; \
	   git --no-pager diff --name-only -- $$paths; \
	   exit 1; \
	 fi; \
	 untracked="$$(git --no-pager status --porcelain --untracked-files=all -- $$paths | awk '$$1 == "??" {print}')"; \
	 if [ -n "$$untracked" ]; then \
	   echo "generated files exist but are not committed (git diff --exit-code is blind to untracked paths):"; \
	   echo "$$untracked"; \
	   echo "run 'git add' and commit them — Phase 15 owns docs/openapi.json and web/src/api/schema.d.ts as generated-but-committed artefacts"; \
	   exit 1; \
	 fi

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

# ── e2e (Phase 18) ───────────────────────────────────────────────────────────
# `@playwright/test` and a `pnpm run e2e` script were dependencies from Phase 0
# with NO suite behind them and no phase owning one — which is why Phase 17 had
# to verify its 60fps criterion by proxy rather than by measurement. Phase 18
# wires up a deliberately SMALL suite: the two confirmation flows this phase
# introduces (pin advance, entitlement rule editor), which are exactly what
# jsdom verifies weakly. It is NOT a retrofit of e2e coverage over Phases 16-17.
#
# Guarded on HANGAR_DB_URL the same way check-identifiers is: the suite runs the
# real binary against a real, seeded, THROWAWAY Postgres — web/e2e/global-setup
# .ts migrates and seeds it, and it will happily rewrite app.setting's
# compatibility pin. Never point this at a database with data in it.
#
# PHASE 19 CLOSE-OUT TRAP, recorded so the next phase does not pay for it
# again. playwright.config.ts listens on 8099 with `reuseExistingServer:
# !CI`. If ANYTHING is already bound to 8099 — most likely a container you
# started to verify the release image by hand — Playwright silently adopts
# it instead of starting its own, and the whole suite runs against a server
# pointed at a DIFFERENT database from the one global-setup just seeded.
# Every spec then fails on an unauthenticated request, which reads exactly
# like a broken session layer and is not one. Check with
# `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8099/healthz`
# before believing an e2e failure, or set HANGAR_E2E_PORT to something else.
.PHONY: e2e
e2e: ## (Phase 18) Playwright: the pin-advance and rule-editor confirmation flows
	@if [ -d web/node_modules ] && [ -n "$${HANGAR_DB_URL:-}" ]; then \
	  cd web && pnpm exec playwright install --with-deps chromium >/dev/null 2>&1 || true; \
	  pnpm run e2e; \
	else $(call skip,e2e,needs web/node_modules and a reachable throwaway HANGAR_DB_URL); fi

# ── invariant gates (see docs/04_RELEASE_GATES.md) ────────────────────────────
# Each guards on the phase artefact that makes it meaningful.
.PHONY: check-money check-identifiers check-alert-sources check-locales check-css check-no-ice check-static-binary check-reachability

check-money:         ## Principle 9 (Phase 1b) — reflection proof of zero float64 on money paths
	@if [ -d internal/domain ] && [ -n "$$(ls -A internal/domain 2>/dev/null)" ]; then \
	  go test -run TestNoFloatOnMoneyPaths ./internal/domain/...; \
	else $(call skip,check-money,internal/domain is empty); fi

check-identifiers:   ## Principle 13 (Phase 2) — every identifier column matches the ingested spec
	@if [ -f "$(SPEC_SNAPSHOT)" ] && [ -n "$${HANGAR_DB_URL:-}" ]; then \
	  go run ./cmd/hangar admin verify-identifier-types --spec "$(SPEC_SNAPSHOT)"; \
	else $(call skip,check-identifiers,needs $(SPEC_SNAPSHOT) and a reachable HANGAR_DB_URL); fi

check-alert-sources: ## §4.4 (Phase 14) — every threshold alert's source route is in the sync set
	@if [ -n "$$(ls -A internal/alerting/catalogue 2>/dev/null)" ]; then \
	  go test -run TestThresholdAlertSourceRoutesScheduled ./internal/alerting/...; \
	else $(call skip,check-alert-sources,alert catalogue not seeded yet); fi

check-locales:       ## §4.6 (Phase 3) — all 9 UI locales resolve to a valid ESI Accept-Language
	@if [ -f internal/i18n/locales.json ]; then \
	  go test -run TestLocaleResolutionExhaustive ./internal/i18n/...; \
	else $(call skip,check-locales,internal/i18n/locales.json absent); fi

check-css:           ## §8.1 (Phase 0) — exactly one .css file may exist under web/src
	@found=$$(find web/src -name '*.css' 2>/dev/null); \
	 [ -n "$$found" ] && echo "$$found" >&2; \
	 n=$$(printf '%s' "$$found" | grep -c . || true); \
	 if [ "$$n" = "0" ]; then $(call skip,check-css,web/src has no stylesheet yet); \
	 elif [ "$$n" != "1" ]; then echo "expected exactly 1 stylesheet (web/src/styles/index.css), found $$n"; exit 1; \
	 else echo "check-css: ok"; fi

check-no-ice:        ## §4.3/§9.2 — no ZeroC Ice or cgo dependency may enter the binary
	@if grep -riE 'zeroc|/ice/|glacier2' go.mod go.sum 2>/dev/null; then \
	  echo "ZeroC Ice must not be linked into the binary — it requires cgo and breaks the static builds of SRS §9.2. Use the out-of-process bridge."; exit 1; \
	else echo "check-no-ice: ok"; fi

check-static-binary: ## §9.2 (Phase 0) — TestStaticBinaryHasNoDynamicLinks: linux/amd64, linux/arm64, windows/amd64
	go test -tags=staticbinary -run TestStaticBinaryHasNoDynamicLinks -timeout=5m ./test/staticbinary/...

# PHASE 20.1. The guard for defect class B20 — a subsystem built, tested,
# and never called. Eighteen phases of that went undetected because the
# suite is green by construction: the package's own tests construct what
# they need, so nothing anywhere fails. `make ci` could not have caught it,
# and now does.
#
# Unguarded, this target is the single highest-value check in the file. It
# is NOT skippable on a missing input the way the others are — its inputs
# are the source tree and the committed allowlist, both of which always
# exist — so it has no $(call skip) branch. If it cannot run, that is a
# failure.
check-reachability:  ## B20 class (Phase 20.1) — every subsystem has a production caller, or is a declared exception
	go test -tags=reachability -timeout=10m ./test/reachability/...

# ── Gate 4 evidence (Phase 20.6) ─────────────────────────────────────────────
#
# Emits docs/gate-evidence/$(VERSION)/gate4/ from the dispatch tables, the
# route classification and the parsed specification. §4.2 requires the
# traceability matrix as a committed artefact; producing it mechanically is
# what stops it being a document that agrees with itself.
#
# DELIBERATELY NOT IN `ci`. The generator EXITS NON-ZERO while any capability
# row is unreachable, which is the correct behaviour — §0.4 says a recorded
# failure is the artefact, and a generator that exited 0 on a failing gate
# would make the file's existence look like the gate's success. Wiring that
# into `make ci` would either break every build until Gate 4 passes, or
# force the exit code to be ignored, and the second is how a gate stops
# meaning anything. Run it at gate time; read the SUMMARY.md it writes.
#
# GATE_VERSION defaults to the phase rather than to $(VERSION). $(VERSION) is
# `git describe --tags --always --dirty`, which on an untagged tree is a SHA
# that changes with every commit — so the artefact would land in a new
# directory each time and the committed evidence would never be updated, only
# accumulated. Pass GATE_VERSION=v1.0.0 at release time.
GATE_VERSION ?= phase-20.8

.PHONY: gate4-evidence
gate4-evidence: ## (Phase 20.6) Emit docs/gate-evidence/$(GATE_VERSION)/gate4 — non-zero exit means the gate is not met
	go run ./tools/gate4-traceability -version "$(GATE_VERSION)"

# ── composite ────────────────────────────────────────────────────────────────
.PHONY: ci ci-strict
ci: verify-generated lint test web-ci e2e check-money check-identifiers check-alert-sources check-locales check-css check-no-ice check-static-binary check-reachability ## Phase 0 exit criterion; strengthens automatically as phases land

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
