// Project HANGAR — Go module definition.
//
// PINS THAT ARE CONTRACTUAL (SRS v3.0 §3.1 — do not float these):
//   go 1.26                              Go 1.26+
//   github.com/danielgtaylor/huma/v2     v2.39.1
//   github.com/riverqueue/river          v0.43.0
//   github.com/jackc/pgx/v5              v5 (major line)
//
// Every other version below is an INDICATIVE FLOOR recorded so that Phase 0 has a
// starting point. Phase 0's first action is `go mod tidy`, which will resolve the
// real latest-compatible patch versions. Do not treat the non-contractual versions
// here as verified — verify them, then commit go.sum.
//
// Module path is a placeholder. Change it once the canonical repository host is
// decided, before Phase 1 (sqlc and openapi-typescript output both embed it).

module github.com/hangar-project/hangar

go 1.26

require (
	// ---- HTTP / API surface (SRS §3.1, §6) ----
	github.com/danielgtaylor/huma/v2 v2.39.1 // CONTRACTUAL PIN
	github.com/go-chi/chi/v5 v5.2.3

	// ---- Persistence (SRS §3.1, §5) ----
	github.com/jackc/pgx/v5 v5.7.6 // CONTRACTUAL major line
	github.com/pressly/goose/v3 v3.26.0
	github.com/shopspring/decimal v1.4.0 // NUMERIC(30,2) arithmetic — Principle 9

	// ---- Job queue (SRS §3.1, §4.2) ----
	github.com/riverqueue/river v0.43.0 // CONTRACTUAL PIN
	github.com/riverqueue/river/riverdriver/riverpgxv5 v0.43.0
	github.com/riverqueue/river/rivertype v0.43.0

	// ---- CLI + configuration (SRS §3.2 cmd/hangar, internal/config) ----
	github.com/spf13/cobra v1.10.1
	github.com/spf13/viper v1.21.0

	// ---- ESI gateway (SRS §4.1) ----
	github.com/dgraph-io/ristretto/v2 v2.3.0 // L1 conditional cache
	github.com/pb33f/libopenapi v0.28.0      // OpenAPI 3.1 ingest; kin-openapi is 3.0-only

	// ---- SSO / crypto (SRS §4.5, §4.8, Principle 8) ----
	github.com/lestrrat-go/jwx/v3 v3.0.11 // offline JWT + cached JWKS
	golang.org/x/oauth2 v0.32.0           // Authorization Code + PKCE S256

	// ---- Alerting + provisioning drivers (SRS §4.3, §4.4) ----
	github.com/wneessen/go-mail v0.7.4    // SMTP delivery channel
	google.golang.org/grpc v1.76.0        // Mumble MurmurRPC driver
	google.golang.org/protobuf v1.36.10   // Mumble MurmurRPC driver
	gopkg.in/yaml.v3 v3.0.1               // CCP notification YAML, generic fallback

	// ---- Observability (SRS §3.1) ----
	github.com/prometheus/client_golang v1.23.2
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.63.0
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/sdk v1.38.0
	go.opentelemetry.io/otel/trace v1.38.0

	// ---- Misc ----
	github.com/google/uuid v1.6.0 // Principle 13 — uuid identifiers are first-class
	golang.org/x/sync v0.17.0     // errgroup: bounded page fan-out (§4.1.4)
)

require (
	// ---- OPTIONAL runtime dependency. Principle 7: Redis is strictly optional and
	// the binary MUST boot and pass Gate 5 with it absent. ----
	github.com/redis/go-redis/v9 v9.15.0
)

require (
	// ---- Test-only ----
	github.com/stretchr/testify v1.11.1
	github.com/testcontainers/testcontainers-go v0.40.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.40.0
)

// Build/codegen tools are pinned in-module so `make ci` is hermetic and CI never
// installs a floating binary. Principle 10: schema drift is a compile error, which
// only holds if the generator version is itself pinned.
tool (
	github.com/pressly/goose/v3/cmd/goose
	github.com/riverqueue/river/cmd/river
	github.com/sqlc-dev/sqlc/cmd/sqlc
)

require github.com/sqlc-dev/sqlc v1.30.0
