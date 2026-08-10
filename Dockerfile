# Project HANGAR — multi-stage production image (SRS §9.1, Phase 0 exit).
#
# Output contract:
#   * one statically linked binary, no libc dependency
#   * the React SPA embedded via embed.FS — the image serves no separate web root
#   * distroless final stage, non-root, read-only-root-filesystem compatible
#
# Gate 5 forbids the administrator compiling anything, so CI builds and pushes
# this image; docker-compose.yml only ever pulls it.

# ─── Stage 1: SPA ────────────────────────────────────────────────────────────
FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# docs/openapi.json is a GENERATED-BUT-COMMITTED artefact (Principle 10): `make
# openapi` writes it from the Huma router and CI fails on a non-empty diff. It
# must therefore exist in the repository before this image can build, which is
# why Phase 0 commits a minimal valid OpenAPI 3.1 stub — the SPA build has no
# way to produce it, and a chicken-and-egg break here would block every image
# build until Phase 15.
COPY docs/openapi.json /src/docs/openapi.json
# internal/i18n/locales.json is the single source of truth vite.config.ts's
# "@i18n/locales.json" alias resolves to (../internal/i18n/locales.json,
# relative to web/ — deliberately outside Vite's default project root, per
# that config's own comment, so the Go and TS sides never drift). Phase 3
# introduced the alias but never added this COPY, so the SPA build stage
# has been unbuildable in a clean Docker context ever since — caught only
# now because nothing had rebuilt the image since before Phase 3 landed.
COPY internal/i18n/locales.json /src/internal/i18n/locales.json
RUN pnpm run build

# ─── Stage 2: Go ─────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# The SPA must land where embed.FS expects it BEFORE compilation.
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
      -o /out/hangar ./cmd/hangar

# ─── Stage 3: runtime ────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/hangar /usr/local/bin/hangar
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/hangar"]
CMD ["serve"]
