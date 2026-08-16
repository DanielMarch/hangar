# Gate 5 — Deployment Usability

**Verdict: FAIL**

The three-command deployment of §5.1 was performed against the release-candidate image on
an empty directory with no repository clone, no Go toolchain and no Node. 12 conditions
were evaluated: 2 failed, 1 passed only in part, and 1 could only be
verified under substitution.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc1` |
| Started | 2026-08-16T01:02:35Z |
| Finished | 2026-08-16T01:04:08Z |
| Procedure | `tools/gate5-deploy/run.sh` |
| Transcript | `transcript.txt` — every command and its output, verbatim |

## Pass conditions

| # | Verdict | Measurement |
| :-- | :-- | :-- |
| 5.3-blank-env | pass | aborts naming the missing variables, no stack trace |
| 5.3-callback-mismatch | **FAIL** | a public-url/callback mismatch is NOT reported at boot: {"time":"2026-08-16T01:02:41.415345783Z","level":"INFO","msg":"hangar serve: starting","version":"v1.0.0-rc1","commit":"3ac0014"} {"time":"2026-08-16T01:02:41.423387578Z","level":"WARN","msg":"hangar: could not verify the schema against the migrations","error":"db: listing existing tables: failed to connect to `user=x database=z`: 127.0.0.1:5432 (127.0.0.1): dial error: dial tcp 127.0.0.1:5432: connect: connection refused"}  |
| 5.3-postgres-not-ready | pass | the migrate service depends_on postgres with condition: service_healthy |
| 5.1 | pass | exactly three commands, no editor step (the two SSO values are prompted for by install.sh) |
| 5.2 | **SUBSTITUTED** | no build: key and no compilation, but the image is NOT pulled from a public registry: ghcr.io/hangar-project/hangar returns 403 and the repository is unpublished. Verified against a locally built image of the same commit |
| 5.4 | pass | /healthz and /readyz both 200 after 9s (budget 300s); migrations ran from the one-shot migrate service |
| 5.5 | pass | services are exactly: hangar migrate postgres  (no redis) |
| 5.6 | pass | the SPA is served by the binary itself on the same port as the API |
| first-boot-alerts | pass | 54 alert types with 4 thresholds on the FIRST boot |
| 5.7 | pass | after a version bump and a second 'docker compose up -d', migrations re-ran and the seeded row survived |
| 5.8 | **PARTIAL** | the static binary ran on a bare debian:12-slim with no toolchain and migrated an external PostgreSQL 18. systemd itself was NOT exercised — this host is Windows and has no systemd; the unit file is deploy/ material and remains unverified |
| 5.8-config-name-collision | **FAIL** | a binary named 'hangar' in its own working directory is parsed as a YAML config file, so §9.2's manual layout boots into an opaque parse error naming neither the file nor the reason |

## What this gate found

Gate 5 had never been run. Running it turned up three defects that no test in the suite could
have caught, because every test builds its own connection string and its own configuration.

**The installer generated a password that broke the deployment.** `install.sh` produced
`POSTGRES_PASSWORD` with `openssl rand -base64 32`, and `docker-compose.yml` interpolates that
value into `HANGAR_DB_URL`. base64's alphabet contains `/`, which terminates a URL's authority, so
`migrate` exited 1 with a parse error and the whole stack failed at the third command — for
roughly one installation in two, non-deterministically. **Fixed in this phase**: both installers
now generate the database password as hex, which is URL-safe by construction. The two 32-byte
base64 secrets are unchanged; neither is ever placed in a URL.

**A `HANGAR_PUBLIC_URL` / callback mismatch is not reported.** §5.3 requires it to surface as a
configuration error naming the expected value. Nothing anywhere compares the two, so the operator
learns about it when a user clicks "log in" and EVE SSO rejects the `redirect_uri` — precisely the
opaque OAuth failure the condition forbids. Not fixed here: it is a change to `internal/config`
and therefore to the binary every gate in this directory measured.

**The binary parses a same-named file in its working directory as its config.** viper is
configured with `SetConfigName("hangar")` + `SetConfigType("yaml")` + `AddConfigPath(".")`, so a
file named exactly `hangar` is read as YAML — and in a manual deployment that file is the binary.
`/opt/hangar/hangar` with `WorkingDirectory=/opt/hangar`, which is what a systemd unit does, boots
into "yaml: control characters are not allowed", naming neither the file nor the reason. The same
bytes under any other name migrate an external PostgreSQL 18 without complaint, so §9.2's static
binary itself is sound.

## What "SUBSTITUTED" means here, and why it is not a pass

§5.1's three commands fetch `docker-compose.yml` and `install.sh` from
`raw.githubusercontent.com/hangar-project/hangar` and pull the image from
`ghcr.io/hangar-project/hangar`. Measured at this release candidate: **404**, **404**, and **403**.
The repository has no git remote and the image has never been pushed.

So the gate was run against a local origin standing in for the published one, and the image the
compose file pulls was supplied locally from this same commit. Everything else — the commands, the
installer, the compose file, the migrations, the healthchecks, the version bump — is exactly what
an operator would get. What this **cannot** show is that the procedure works from a blank host,
because the artefacts a blank host would fetch do not exist. Condition 5.2 is recorded as
substituted for that reason and not as met.

## §5.8, and the one part of it that was not exercised

The static `linux/amd64` binary was run on a bare `debian:12-slim` with no toolchain of any kind,
against an external PostgreSQL 18 in its own container, and migrated it successfully. **systemd
itself was not exercised** — this host is Windows and has no systemd — so the unit file remains
unverified. That is a gap in the evidence, not a claim about the unit file.
