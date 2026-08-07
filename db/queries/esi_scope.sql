-- name: UpsertEsiScope :exec
-- scope is opaque and never parsed (SRS v3.1 §4.5). Any string ESI hands
-- back in a security block is recorded, first-seen-wins.
INSERT INTO app.esi_scope (scope)
VALUES ($1)
ON CONFLICT (scope) DO NOTHING;

-- name: ListUnacknowledgedEsiScopes :many
SELECT * FROM app.esi_scope WHERE acknowledged_at IS NULL ORDER BY first_seen_at;

-- name: AcknowledgeEsiScope :exec
UPDATE app.esi_scope SET acknowledged_at = now() WHERE scope = $1;

-- name: GetEsiScope :one
SELECT * FROM app.esi_scope WHERE scope = $1;
