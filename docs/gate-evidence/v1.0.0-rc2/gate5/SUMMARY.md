# Gate 5 — Deployment Usability

**Verdict: PASS (WITH SUBSTITUTION)**

The three-command deployment of §5.1 was performed against the release-candidate image on
an empty directory with no repository clone, no Go toolchain and no Node. 12 conditions
were evaluated: 0 failed, 1 passed only in part, and 1 could only be
verified under substitution.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc2` |
| Started | 2026-08-16T21:28:39Z |
| Finished | 2026-08-16T21:29:49Z |
| Procedure | `tools/gate5-deploy/run.sh` |
| Transcript | `transcript.txt` — every command and its output, verbatim |

## Pass conditions

| # | Verdict | Measurement |
| :-- | :-- | :-- |
| 5.3-blank-env | pass | aborts naming the missing variables, no stack trace |
| 5.3-callback-mismatch | pass | reported at boot as a configuration error naming the EXPECTED callback (https://hangar.example.com/auth/callback), not an opaque OAuth failure |
| 5.3-postgres-not-ready | pass | the migrate service depends_on postgres with condition: service_healthy |
| 5.1 | pass | exactly three commands, no editor step (the two SSO values are prompted for by install.sh) |
| 5.2 | **SUBSTITUTED** | no build: key and no compilation, but the image is NOT pulled from a public registry: ghcr.io/hangar-project/hangar returns 403, raw.githubusercontent.com/hangar-project/hangar 404s, and 'git remote -v' is empty. DECIDED IN PHASE 22 (defect B-12): recorded as PERMANENTLY SUBSTITUTED for this release candidate, because publishing is an operator action with credentials this session does not hold, not a code change. Verified against a locally built image of the same commit; see docs/PRE_V1_OPEN_ITEMS.md B-12 for exactly what remains |
| 5.4 | pass | /healthz and /readyz both 200 after 9s (budget 300s); migrations ran from the one-shot migrate service |
| 5.5 | pass | services are exactly: hangar migrate postgres  (no redis) |
| 5.6 | pass | the SPA is served by the binary itself on the same port as the API |
| first-boot-alerts | pass | 54 alert types with 4 thresholds on the FIRST boot |
| 5.7 | pass | after a version bump and a second 'docker compose up -d', migrations re-ran and the seeded row survived |
| 5.8 | **PARTIAL** | the static binary ran on a bare debian:12-slim with no toolchain and migrated an external PostgreSQL 18. systemd itself was NOT exercised — this host is Windows and has no systemd; the unit file is deploy/ material and remains unverified |
| 5.8-config-name-collision | pass | the binary named 'hangar' in its own working directory (§9.2's documented layout) is not mistaken for a config file, and migrated the external PostgreSQL 18 from there |

## What this gate found the first time it was run, and where those defects are now

Gate 5 had never been run before v1.0.0-rc1. Running it turned up three defects that no test in
the suite could have caught, because every test builds its own connection string and its own
configuration. All three are closed as of v1.0.0-rc2.

**The installer generated a password that broke the deployment.** `install.sh` produced
`POSTGRES_PASSWORD` with `openssl rand -base64 32`, and `docker-compose.yml` interpolates that
value into `HANGAR_DB_URL`. base64's alphabet contains `/`, which terminates a URL's authority, so
`migrate` exited 1 with a parse error and the whole stack failed at the third command — for
roughly one installation in two, non-deterministically. **Fixed in Phase 21**: both installers
now generate the database password as hex, which is URL-safe by construction. The two 32-byte
base64 secrets are unchanged; neither is ever placed in a URL.

**A `HANGAR_PUBLIC_URL` / callback mismatch was not reported** (defect B-8). §5.3 requires it to
surface as a configuration error naming the expected value. Nothing anywhere compared the two, so
the operator learned about it when a user clicked "log in" and EVE SSO rejected the
`redirect_uri` — precisely the opaque OAuth failure the condition forbids. **Fixed in Phase 22**:
`internal/config.Validate` compares them at boot and the error names
`${HANGAR_PUBLIC_URL}/auth/callback`, because an operator who set one of them wrong needs the
other. Condition `5.3-callback-mismatch` now checks for that expected value specifically, not
merely for the word "callback".

**The binary parsed a same-named file in its working directory as its config** (defect B-7). viper
was configured with `SetConfigName("hangar")` + `SetConfigType("yaml")` + `AddConfigPath(".")`, and
the config TYPE is what enables viper's extensionless fallback — so a file named exactly `hangar`
was read as YAML, and in a manual deployment that file is the binary. `/opt/hangar/hangar` with
`WorkingDirectory=/opt/hangar`, which is what a systemd unit produces, booted into "yaml: control
characters are not allowed", naming neither the file nor the reason. **Fixed in Phase 22** by
dropping the config-type declaration; `./hangar.yaml` and `/etc/hangar/hangar.yaml` are still found
by extension.

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

**Phase 22 made that an explicit decision rather than an open item** (defect B-12). Publishing the
repository, the compose file, the installer and the image is a release action requiring credentials
and an outward-facing push; it is not a code change, and nothing in the codebase can close it.
Condition 5.2 is therefore recorded as **permanently substituted for this release candidate**. What
that costs is precise and worth stating rather than leaving to inference: **the documented
three-command deployment cannot be performed by anybody who is not this repository.** Every other
part of it is verified — 5.1, 5.3, 5.4, 5.5, 5.6, 5.7 and 5.8 all measured against the real
artefacts — so the moment the three URLs resolve, 5.2 becomes a pass with no further work. Until
then Gate 5 is not fully met, and §8's "release blocks on all seven" applies.

## §5.8, and the one part of it that was not exercised

The static `linux/amd64` binary was run on a bare `debian:12-slim` with no toolchain of any kind,
against an external PostgreSQL 18 in its own container, and migrated it successfully. **systemd
itself was not exercised** — this host is Windows and has no systemd — so the unit file remains
unverified. That is a gap in the evidence, not a claim about the unit file.
