# Project HANGAR

A ground-up rebuild of the EVE Online corporation management tool **SeAT**: a single Go binary
plus PostgreSQL 18, with an embedded React 19 SPA.

HANGAR exists to fix three structural problems in the legacy system — ESI rate-limit handling,
architectural bottlenecks, and abandoned third-party access-provisioning plugins — while
delivering full feature parity across **58 verified capabilities** against a measured legacy
footprint of 106 distinct ESI routes, 72 UI controllers, 54 alert types, 70 ESI scopes and
9 locales.

> **Status: release candidate.** The application is built and running. Every release gate that
> has been run has its evidence committed under `docs/gate-evidence/<version>/`, one directory
> per candidate, each carrying the blocking artefact the gate is defined by rather than a
> summary of it — so a reader can disagree with the verdict by reading the measurement.
> `docs/PRE_V1_OPEN_ITEMS.md` is the honest register of what is not finished, and of what has
> been decided rather than built.

---

## Documents

| Document | What it covers |
| :-- | :-- |
| [`docs/00_SRS_v3.1.md`](docs/00_SRS_v3.1.md) | **The authoritative specification.** Supersedes v3.0, which contains eleven corrected defects and must not be implemented from |
| [`docs/01_ARCHITECTURE.md`](docs/01_ARCHITECTURE.md) | System design: ESI gateway, the cluster-shared consumption ledger, SSO and offline JWT validation, provisioning and the <60 s revocation SLO, the API layer, the frontend, and the defect register |
| [`docs/02_DATABASE_SCHEMA.md`](docs/02_DATABASE_SCHEMA.md) | PostgreSQL 18 schema: 51 platform tables, ~78 domain projection tables, typing rules for money and identifiers, partitioning, the SDE atomic swap |
| [`docs/03_IMPLEMENTATION_ROADMAP.md`](docs/03_IMPLEMENTATION_ROADMAP.md) | Phases 0–20: files, legacy references, design notes, edge cases, named exit tests, and a ready-to-paste prompt seed per phase |
| [`docs/04_RELEASE_GATES.md`](docs/04_RELEASE_GATES.md) | The seven release gates, their harnesses, pass conditions and evidence artefacts |

Start with `00`, then `01`, then the phase you are implementing in `03`.

---

## Architectural principles

These are binding. Full statements live in the SRS; the mechanisms that enforce them are in
`docs/01_ARCHITECTURE.md` §17.

1. **The spec is the schedule.** Route TTLs, cache modes, rate-limit groups, required roles,
   scopes and pagination style are ingested from the live OpenAPI document, never hardcoded.
2. **Never guess what the server tells you.** Rate and error limits reconcile against
   authoritative response headers; the server always wins.
3. **Failure is scoped.** One character, one route. Never the installation.
4. **Freshness is per-route.** Per-endpoint TTLs and cache modes, not global intervals.
5. **Conditional requests default.** 304 is the cheapest correct request.
6. **One API.** The SPA and third parties consume the identical generated REST surface.
7. **Two-service deployment.** One Go binary, one PostgreSQL. Redis is strictly optional.
8. **Secrets never leave the gateway.** Refresh tokens are envelope-encrypted at rest.
9. **Money is exact.** `NUMERIC(30,2)` in Postgres, `decimal.Decimal` in Go, strings in JSON.
10. **Schema drift is a compile error.** sqlc and `openapi-typescript`, diffed in CI.
11. **Revocations are synchronous.** p99 under 60 seconds, enqueued in the mutating transaction.
12. **Compatibility is pinned, discovery is not.** Two distinct dates that must never be conflated.
13. **Identifiers are typed by the spec.** `int64` → `bigint`, `uuid` → `uuid`. Never coerced.
14. **External vocabularies and grammars are open.** Enums, notification types and scope strings
    that originate outside HANGAR are `text`, never validated against a pattern or closed set.
    Vocabularies HANGAR itself owns may be closed.
15. **The baseline is measured, not asserted.**
16. **Shared limits require shared state.** Any budget ESI enforces installation-wide is
    accounted installation-wide. Per-replica accounting of a global budget is prohibited.

---

## Stack

**Backend** — Go 1.26+ · Huma v2.39.1 (OpenAPI 3.1 on `net/http` + chi) · PostgreSQL 18 ·
pgx v5 + sqlc · Goose v3 · River v0.43.0 · EVE SSO OAuth 2.0 PKCE S256 with offline JWT
validation · `log/slog` + OpenTelemetry + Prometheus

**Frontend** — React 19 · TypeScript 5.9 · Vite 7 · TanStack Router v1 / Query v5 / Table v8 /
Virtual v3 · Zustand v5 · Tailwind CSS 4 · shadcn/ui (Radix primitives) · embedded via `embed.FS`

**Deployment** — turnkey Docker Compose, or a static binary for linux/amd64, linux/arm64 and
windows/amd64

---

## Layout

```
cmd/hangar/        serve · work · schedule · migrate · admin
internal/          config esi sso scopes crypto sync domain store rbac
                   alerting provisioning events api i18n sde telemetry
db/                migrations · queries · seed
web/               React 19 SPA
docs/              the four design documents
deploy/            install scripts, Helm chart
testdata/          recorded ESI responses, notification corpus, legacy v2 corpus
```

---

## Getting started (once Phase 0 lands)

```bash
cp .env.example .env
```

Fill in `HANGAR_SSO_CLIENT_ID`, `HANGAR_SSO_CLIENT_SECRET`, `POSTGRES_PASSWORD`, and generate
the two keys with `openssl rand -base64 32`. Then:

```bash
docker compose up -d
```

For development:

```bash
make generate && make migrate-up && make build
```

`make ci` is the full gate: generated-artefact freshness, lint, tests, the SPA build, and the six
invariant checks (money, identifier types, alert source routes, locales, stylesheet count, no-Ice).

**`make ci` is progressive.** Each invariant check skips while its input is absent and starts
enforcing the moment the phase that introduces it lands, so the same target passes at Phase 0 and
is fully binding by Phase 20. `make ci-strict` turns every skip into a failure — CI uses it from
Phase 15 and on every release tag, so a check cannot silently skip forever.

**On Windows**, run `make` from Git Bash or WSL; the Makefile uses POSIX shell. `go build`,
`go test` and `docker compose` all work natively from PowerShell if you prefer to skip `make`.

---

## Specification defects found and corrected

Architectural review of SRS v3.0 found eleven defects. All are corrected in **v3.1**; the
register with full rationale is in `docs/01_ARCHITECTURE.md` §18.

| ID | Defect in v3.0 | Correction in v3.1 |
| :-- | :-- | :-- |
| **B1** | Consumption-ledger state was per-replica with reactive header reconciliation, so N replicas could spend N× the ESI budget before any correction landed | Ledger is cluster-shared through Postgres; the in-process fast path is selected automatically from a replica heartbeat registry. No operator-settable divisor — a knob that can be set wrongly reintroduces the defect. Gate 1 runs at N=1 and N=3 |
| **B2** | "~48 core tables" covered the platform tier only; the real schema is ≈129 tables | 51 platform + ≈78 domain; Phase 1 split into 1a and 1b |
| **B3** | "No custom `.css` files" does not build under Tailwind 4's CSS-first configuration | Exactly one sanctioned stylesheet with restricted contents, CI-enforced |
| **B4** | In-binary ZeroC Ice was required, but no maintained Go binding exists and CGO breaks the static builds mandated elsewhere in the same document | gRPC is the only in-binary Mumble driver; Ice ships as an optional out-of-process bridge on the same contract |
| **B5** | Error limit declared installation-wide while its state was declared per-replica — at N replicas that permits N×100 errors per window | Error budget explicitly cluster-shared, with resume hysteresis |
| **B6** | Baseline counts asserted but never reproduced, contradicting Principle 15 while Gate 4 depended on them | Phase 0 measures legacy HEAD into `docs/BASELINE.md`; Gate 4 verifies against that file |
| **B7** | The locale resolution table was needed by both Go and the SPA with no single owner | One `internal/i18n/locales.json`, embedded in Go and imported by Vite |
| B8–B11 | Cost-table precedence between 429 and 4XX; whether ledger entries are stamped at request or response; Wars as an alert domain with no data source; `app.permission` appearing to contradict Principle 14 | Each resolved explicitly in v3.1 §0 |

A new **Principle 16** was added to prevent B1 and B5 recurring: any budget ESI enforces across
the installation must be accounted for across the installation.

---

## Declared scope reductions

Three legacy capabilities are **intentionally not replaced**, so that "full feature parity" is not
read as covering them:

1. **The in-process plugin model.** SeAT allowed third-party Laravel service providers to inject
   routes, migrations, jobs and views into the running application. HANGAR replaces this with an
   out-of-process model: the generated REST surface, scoped API tokens, and signed outbound
   webhooks. No third-party code runs inside the HANGAR binary.
2. **The versioned `/api/v2` surface.** Superseded by `/api/v1`. A time-boxed, read-only
   compatibility shim ships in v1.0 and is verified by Gate 7.
3. **In-binary ZeroC Ice support for Mumble.** Delivered instead as an optional out-of-process
   bridge speaking the same gRPC contract. No Ice runtime is linked into the binary.
