# Registering the EVE SSO application

What to enter at <https://developers.eveonline.com/applications>, and where the resulting
credentials go.

Everything below is **derived from the repository**, not recalled: the callback path from
`internal/config`'s default and `internal/api/v1/auth.go`'s route registration, the scope set from
`internal/sync/worker.SyncSet()` cross-referenced against the embedded spec snapshot. Regenerate
the scope list with:

```bash
go run ./tools/scopedump
```

---

## 1. Connection type

**Authentication & API Access.** HANGAR needs refresh tokens; an "Authentication Only"
registration issues none, and every authenticated ESI route would fail at the first sync.

## 2. Callback URL

```
http://localhost:8080/auth/callback
```

`http` with `localhost` is accepted by the portal (its own note: *"use only for `localhost`"*).

Three things must agree, and the SSO handshake fails with a `redirect_uri` mismatch if any one of
them drifts:

| Setting | Value |
| :-- | :-- |
| The portal's Callback URL | `http://localhost:8080/auth/callback` |
| `HANGAR_SSO_CALLBACK_URL` | `http://localhost:8080/auth/callback` (this is already the default) |
| `HANGAR_PUBLIC_URL` | `http://localhost:8080` — origin only, no path |

The path `/auth/callback` is not configurable: `RegisterAuthRedirects` mounts it literally
(`internal/api/v1/auth.go`). Only the scheme, host and port change between deployments.

For a real deployment later, register a **second** application with the public HTTPS callback
rather than editing this one — the localhost registration stays useful for development, and CCP
allows only one callback URL per application.

## 3. Scopes — 46, and why each

HANGAR's sync set is 80 routes. Forty-five of them declare a scope on their `GET` operation; the
calendar routes add one more that the current code masks (see B38 below). That is the complete
set, and it is a **read-only** set.

> **Do not tick the write scopes.** `esi-characters.write_contacts.v1`,
> `esi-mail.send_mail.v1` and `esi-mail.organize_mail.v1` appear on the *same paths* HANGAR reads,
> so a naive "everything on these paths" derivation includes them. HANGAR never issues a
> non-`GET` ESI call — the `/api/v2` shim is read-only by construction and the sync workers only
> fetch — so granting them would ask every user for a permission the software cannot exercise.

Grouped as the portal's tree presents them:

| Group | Scopes |
| :-- | :-- |
| `esi-assets` | `read_assets.v1` |
| `esi-calendar` | `read_calendar_events.v1` |
| `esi-characters` | `read_agents_research.v1`, `read_blueprints.v1`, `read_contacts.v1`, `read_corporation_roles.v1`, `read_fatigue.v1`, `read_loyalty.v1`, `read_medals.v1`, `read_notifications.v1`, `read_standings.v1`, `read_titles.v1` |
| `esi-clones` | `read_clones.v1`, `read_implants.v1` |
| `esi-contracts` | `read_character_contracts.v1`, `read_corporation_contracts.v1` |
| `esi-corporations` | `read_blueprints.v1`, `read_contacts.v1`, `read_container_logs.v1`, `read_corporation_membership.v1`, `read_divisions.v1`, `read_facilities.v1`, `read_medals.v1`, `read_projects.v1`, `read_standings.v1`, `read_starbases.v1`, `read_structures.v1`, `read_titles.v1`, `track_members.v1` |
| `esi-industry` | `read_character_jobs.v1`, `read_character_mining.v1`, `read_corporation_jobs.v1`, `read_corporation_mining.v1` |
| `esi-location` | `read_location.v1`, `read_online.v1`, `read_ship_type.v1` |
| `esi-mail` | `read_mail.v1` |
| `esi-markets` | `read_character_orders.v1`, `read_corporation_orders.v1` |
| `esi-planets` | `manage_planets.v1`, `read_customs_offices.v1` |
| `esi-skills` | `read_skillqueue.v1`, `read_skills.v1` |
| `esi-structures` | `read_corporation.v1` |
| `esi-wallet` | `read_character_wallet.v1`, `read_corporation_wallets.v1` |

`esi-planets.manage_planets.v1` looks like a write scope and is not: it is CCP's name for the
scope that **reads** planetary interaction, and `/characters/{id}/planets` declares it on `GET`.

> ### ⚠ `esi-wallet.read_corporation_wallets.v1` — plural, not singular
>
> The developer portal offers **`esi-wallet.read_corporation_wallet.v1`** (singular) as well.
> That scope is **not declared by the current ESI spec** — checked against
> `esi.evetech.net/meta/openapi.json`, which declares only the plural — and ticking it instead
> of the plural is the single most likely way to get this registration wrong. The two sit
> adjacent in the portal's list and differ by one character.
>
> The consequence is out of all proportion to the typo: the *whole* authorization is refused with
> `invalid_scope`, after the user has entered their password and 2FA code, and the error names
> only that one scope. This exact mistake cost a debugging session during Phase 20.2.
>
> Verify with:
> ```bash
> curl -s https://esi.evetech.net/meta/openapi.json | grep -o 'esi-wallet[^"]*'
> ```

`publicData` is not required. HANGAR's public routes (corporation history, alliance names) are
unauthenticated, and the scope grants nothing the sync set uses.

### If SSO answers `invalid_scope`

```json
{"error":"invalid_scope","error_description":"The requested 'esi-…' scope is not valid."}
```

This means the **application registration** does not have that scope enabled. It does not mean
the scope name is wrong — verify against the spec's own canonical list before assuming otherwise:

```bash
curl -s https://esi.evetech.net/meta/openapi.json \
  | python -c "import json,sys; print(*sorted(json.load(sys.stdin)['components']['securitySchemes']['OAuth2']['flows']['authorizationCode']['scopes']), sep='\n')"
```

Two things make this error worse than it looks, and both are worth knowing before you debug it:

1. **It is raised after authentication**, not before. The user types their password and their 2FA
   code, and only then is the authorization refused. Nothing is wrong with the credentials.
2. **It names one scope at a time.** Reconciling a registration by trial and error therefore costs
   a full login round trip per missing scope. Enable all of them in one edit instead.

If the reported scope is the **last one alphabetically** in the request, suspect that the
application has *no* scopes enabled rather than one missing — SSO reports the last scope it
validated, so an empty registration looks identical to a single bad scope at the end of the list.

`HANGAR_SSO_SCOPES` is the escape hatch for an application deliberately registered with a
narrower set: it replaces the derived set verbatim. Routes whose scopes are then absent fail
individually at sync time, which is visible on the sync board — far better than a login surface
that cannot be used at all. Leave it unset in the normal case.

### Scopes are stored verbatim, so over-granting is visible

`internal/scopes` treats a scope as an opaque string and `app.esi_scope` is a `text` primary key
(Gate 6 condition (c) is precisely that an unrecognised scope grammar must survive un-parsed). A
scope granted here that HANGAR does not use is therefore recorded and surfaced on
`/admin/scopes/unknown` rather than silently ignored — useful, but not a reason to grant extra.

## 4. Where the credentials go

The portal issues a **Client ID** and a **Secret Key**:

```bash
HANGAR_SSO_CLIENT_ID=<Client ID>
HANGAR_SSO_CLIENT_SECRET=<Secret Key>
```

Both are read by `internal/config` and reach `sso.OAuthConfig` through `ssoOAuthConfig`
(`cmd/hangar/sso.go`). The secret is wrapped in `config.Secret`, so it is redacted from logs and
from `/admin` config views rather than merely "not logged on purpose".

The Secret Key is shown once. It is not recoverable from the portal afterwards — only
regenerable, which invalidates the old one.

---

## Two defects this registration surfaced

Recorded here because both are reachable from this page's subject, and both are open.

**B37 — HANGAR requests no scopes at login.** `internal/api/v1/auth.go` calls
`flow.BeginLogin(ctx, []string{}, …)` at both call sites: the login redirect and the
character-reauthorize handler. An empty scope list is exactly the case the developer portal warns
about — *"your application will only be able to authenticate users, and no refresh tokens will be
issued"*. So no matter which scopes are ticked here, the authorization URL asks for none of them,
no refresh token comes back, and every authenticated route in the sync layer has nothing to call
with. Ticking the 46 scopes above is necessary and is **not sufficient**; the login flow has to
request them too. Closed by Phase 20.2.

**B38 — two sync-set paths do not exist in the spec.** `internal/sync/worker` registers
`/characters/{character_id}/calendar/events` where ESI has `/characters/{character_id}/calendar`,
and `/corporations/{corporation_id}/projects/{project_id}/contributions` where ESI has
`…/contributors`. Both look like a plural inferred from the resource name — the exact failure
Principle 5 ("`upstream_path` is stored verbatim, never derived or pluralised") exists to prevent.
Neither route can ever return data, and neither would be noticed, because the handlers behind them
are part of B30 and are never dispatched anyway. `esi-calendar.read_calendar_events.v1` is in the
list above because the *correct* path needs it. Closed by Phase 20.2 alongside B30's dispatch.
