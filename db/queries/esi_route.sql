-- app.esi_route, app.esi_route_scope, app.esi_route_role, app.esi_pin_history
-- (02_DATABASE_SCHEMA.md §4.3 #21, #23-#25). app.esi_cache_entry's queries
-- (#26) moved to db/queries/esi_cache.sql in Phase 3, once the conditional
-- cache gained its own consumer (internal/esi/cache) worth naming a file after.

-- name: UpsertEsiRoute :one
INSERT INTO app.esi_route AS t (
    operation_id, method, upstream_path, cache_age, cache_mode,
    rate_limit_group, rate_limit_max, rate_limit_window, pagination_style,
    compatibility_date, blocked_by_pin, spec_fragment, identifier_types
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
ON CONFLICT (operation_id) DO UPDATE
   SET method              = EXCLUDED.method,
       upstream_path        = EXCLUDED.upstream_path,
       cache_age            = EXCLUDED.cache_age,
       cache_mode           = EXCLUDED.cache_mode,
       rate_limit_group     = EXCLUDED.rate_limit_group,
       rate_limit_max       = EXCLUDED.rate_limit_max,
       rate_limit_window    = EXCLUDED.rate_limit_window,
       pagination_style     = EXCLUDED.pagination_style,
       compatibility_date   = EXCLUDED.compatibility_date,
       blocked_by_pin       = EXCLUDED.blocked_by_pin,
       spec_fragment        = EXCLUDED.spec_fragment,
       identifier_types     = EXCLUDED.identifier_types,
       updated_at           = now()
 WHERE (t.method, t.upstream_path, t.cache_age, t.cache_mode, t.rate_limit_group,
        t.rate_limit_max, t.rate_limit_window, t.pagination_style,
        t.compatibility_date, t.blocked_by_pin, t.spec_fragment, t.identifier_types)
    IS DISTINCT FROM
       (EXCLUDED.method, EXCLUDED.upstream_path, EXCLUDED.cache_age, EXCLUDED.cache_mode,
        EXCLUDED.rate_limit_group, EXCLUDED.rate_limit_max, EXCLUDED.rate_limit_window,
        EXCLUDED.pagination_style, EXCLUDED.compatibility_date, EXCLUDED.blocked_by_pin,
        EXCLUDED.spec_fragment, EXCLUDED.identifier_types)
RETURNING *;

-- name: GetEsiRouteByOperationID :one
SELECT * FROM app.esi_route WHERE operation_id = $1;

-- name: GetEsiRouteByID :one
-- Phase 6's app.sync_subscription.route_id and the KindSyncRoute River job
-- payload (internal/sync/planner.SyncJobArgs) both carry route_id, not
-- operation_id — the worker (Phase 7+) re-reads the route by this key.
SELECT * FROM app.esi_route WHERE route_id = $1;

-- name: GetEsiRouteByMethodAndPath :one
-- PHASE 20.5 (B30). The asset-names enrichment makes a SECOND upstream call
-- from inside the assets sync, and Principle 5 says the upstream path is
-- taken verbatim from the catalogue, never hand-built. The subscription only
-- carries the LIST route's route_id, so the POST route is looked up by the
-- pair that identifies it in the spec. Deliberately not by operation_id:
-- operation ids are CCP's own and have changed under HANGAR before, whereas
-- (method, path) is the identity app.esi_route is unique on.
SELECT * FROM app.esi_route WHERE method = $1 AND upstream_path = $2;

-- name: ListEsiRoutes :many
SELECT * FROM app.esi_route WHERE retired_at IS NULL ORDER BY upstream_path;

-- name: ListBlockedEsiRoutes :many
SELECT * FROM app.esi_route WHERE blocked_by_pin ORDER BY upstream_path;

-- name: ListSchedulableEsiRoutes :many
-- Phase 2's "excluded from scheduling" contract: a blocked-by-pin or
-- retired route must never appear here, however it does appear in
-- ListEsiRoutes/ListBlockedEsiRoutes for administrator visibility
-- (/admin/esi/catalogue/blocked). Phase 6's claim query is expected to
-- join sync_subscription against this, not against ListEsiRoutes.
SELECT * FROM app.esi_route
 WHERE NOT blocked_by_pin AND retired_at IS NULL
 ORDER BY upstream_path;

-- name: RetireEsiRoute :exec
UPDATE app.esi_route SET retired_at = now(), updated_at = now() WHERE route_id = $1;

-- name: AddEsiRouteScope :exec
INSERT INTO app.esi_route_scope (route_id, scope)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListEsiRouteScopes :many
SELECT scope FROM app.esi_route_scope WHERE route_id = $1 ORDER BY scope;

-- name: ListScopesForRoutePaths :many
-- The login scope set (defect B37): every scope declared by any route
-- HANGAR actually syncs. Driving this from the catalogue rather than a Go
-- constant is the same commitment as everywhere else — the spec is the
-- schedule. A scope newly attached upstream to a route already in the sync
-- set is requested at the next login after the next ingest, with no code
-- change, which is the property Gate 6 exists to prove for the catalogue
-- and which a hand-maintained list would quietly lose.
--
-- METHOD = 'GET' IS LOad-BEARING, NOT TIDINESS. ESI declares write scopes
-- on the SAME upstream_path as the reads: /characters/{id}/contacts carries
-- esi-characters.read_contacts.v1 on GET and .write_contacts.v1 on
-- POST/PUT/DELETE, and /characters/{id}/mail carries send_mail on POST.
-- HANGAR issues no non-GET ESI call anywhere, so dropping this predicate
-- would make every user grant three write permissions the software cannot
-- exercise. Measured: 48 scopes without it, 45 with.
--
-- Retired routes are excluded: a route that has vanished from the spec
-- keeps its row (never deleted) but must not keep asking users for a scope
-- nothing can call.
SELECT DISTINCT rs.scope
  FROM app.esi_route_scope rs
  JOIN app.esi_route r USING (route_id)
 WHERE r.method = 'GET'
   AND r.retired_at IS NULL
   AND r.upstream_path = ANY(sqlc.arg(paths)::text[])
 ORDER BY rs.scope;

-- name: AddEsiRouteRole :exec
INSERT INTO app.esi_route_role (route_id, role)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListEsiRouteRoles :many
SELECT role FROM app.esi_route_role WHERE route_id = $1 ORDER BY role;

-- name: RecordEsiPinAdvance :one
INSERT INTO app.esi_pin_history (old_pin, new_pin, actor, route_diff)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListEsiPinHistory :many
SELECT * FROM app.esi_pin_history ORDER BY advanced_at DESC LIMIT sqlc.arg(page_size);
