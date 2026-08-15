-- app.asset, app.asset_location (02_DATABASE_SCHEMA.md §5.3).

-- name: UpsertAsset :one
INSERT INTO app.asset AS t (
    owner_kind, owner_id, item_id, type_id, location_id, location_type,
    location_flag, quantity, is_singleton, is_blueprint_copy, name, x, y, z
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (owner_kind, owner_id, item_id) DO UPDATE
   SET type_id           = EXCLUDED.type_id,
       location_id       = EXCLUDED.location_id,
       location_type     = EXCLUDED.location_type,
       location_flag     = EXCLUDED.location_flag,
       quantity          = EXCLUDED.quantity,
       is_singleton      = EXCLUDED.is_singleton,
       is_blueprint_copy = EXCLUDED.is_blueprint_copy,
       name              = EXCLUDED.name,
       x = EXCLUDED.x, y = EXCLUDED.y, z = EXCLUDED.z,
       deleted_at        = NULL,   -- an asset that reappears is restored, never re-inserted
       updated_at        = now()
 WHERE (t.type_id, t.location_id, t.location_type, t.location_flag, t.quantity,
        t.is_singleton, t.is_blueprint_copy, t.name, t.x, t.y, t.z, t.deleted_at)
    IS DISTINCT FROM
       (EXCLUDED.type_id, EXCLUDED.location_id, EXCLUDED.location_type, EXCLUDED.location_flag,
        EXCLUDED.quantity, EXCLUDED.is_singleton, EXCLUDED.is_blueprint_copy, EXCLUDED.name,
        EXCLUDED.x, EXCLUDED.y, EXCLUDED.z, NULL::timestamptz)
RETURNING *;

-- name: SoftDeleteAssetsNotIn :exec
-- Reconciliation: anything not present in the latest sync page set is
-- soft-deleted, never DELETEd (02_DATABASE_SCHEMA.md §3.6).
UPDATE app.asset SET deleted_at = now(), updated_at = now()
 WHERE owner_kind = $1 AND owner_id = $2 AND deleted_at IS NULL
   AND NOT (item_id = ANY(sqlc.arg(keep_item_ids)::bigint[]));

-- name: SetAssetName :execrows
-- PHASE 20.5 (B30). Asset names arrive in a SECOND upstream call —
-- POST /{owner}/{id}/assets/names, whose request body is the item ids the
-- assets LIST call just returned — so they are applied over rows the list
-- sync has already committed rather than folded into UpsertAsset. That
-- ordering is ESI's, not a choice: there is nothing to ask for names for
-- until the list has been read.
--
-- Only nameable items ever appear here (ESI returns a name only for a
-- singleton container or ship), and the IS DISTINCT FROM guard makes an
-- unchanged name a zero-row write, so a steady-state pass over a hangar
-- full of named cans writes nothing. A soft-deleted row is deliberately NOT
-- excluded: naming an item that has since gone is harmless, and adding the
-- predicate would make the update's row count depend on delete timing.
UPDATE app.asset
   SET name = sqlc.arg(name), updated_at = now()
 WHERE owner_kind = $1 AND owner_id = $2 AND item_id = sqlc.arg(item_id)
   AND name IS DISTINCT FROM sqlc.arg(name);

-- name: GetAsset :one
SELECT * FROM app.asset WHERE owner_kind = $1 AND owner_id = $2 AND item_id = $3;

-- name: ListAssetsByOwner :many
SELECT * FROM app.asset
 WHERE owner_kind = $1 AND owner_id = $2 AND deleted_at IS NULL
   AND item_id > sqlc.arg(after_item_id)
 ORDER BY item_id
 LIMIT sqlc.arg(page_size);

-- name: AssetTree :many
-- Single-query recursive tree for GET /{owner}/{id}/assets/tree/{location_id}
-- (02_DATABASE_SCHEMA.md §5.3, the Phase 1b exit fixture). Both the depth
-- bound and the cycle guard are required: a torn sync can introduce a loop
-- and the query must degrade to a truncated tree, not run unbounded.
WITH RECURSIVE tree AS (
    SELECT a.*, 1 AS depth, ARRAY[a.item_id] AS path
      FROM app.asset a
     WHERE a.owner_kind = sqlc.arg(owner_kind) AND a.owner_id = sqlc.arg(owner_id)
       AND a.location_id = sqlc.arg(location_id) AND a.deleted_at IS NULL
    UNION ALL
    SELECT c.*, t.depth + 1, t.path || c.item_id
      FROM app.asset c
      JOIN tree t ON c.location_id = t.item_id
     WHERE c.owner_kind = sqlc.arg(owner_kind) AND c.owner_id = sqlc.arg(owner_id) AND c.deleted_at IS NULL
       AND t.depth < sqlc.arg(max_depth)::int          -- bound; containers can cycle after a bad sync
       AND NOT c.item_id = ANY(t.path)                 -- cycle guard
)
SELECT owner_kind, owner_id, item_id, type_id, location_id, location_type,
       location_flag, quantity, is_singleton, is_blueprint_copy, name, x, y, z,
       deleted_at, updated_at, depth
  FROM tree
 ORDER BY depth, item_id;

-- name: UpsertAssetLocation :one
INSERT INTO app.asset_location AS t (
    owner_kind, owner_id, item_id, root_location_id, root_location_type, system_id
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (owner_kind, owner_id, item_id) DO UPDATE
   SET root_location_id   = EXCLUDED.root_location_id,
       root_location_type = EXCLUDED.root_location_type,
       system_id          = EXCLUDED.system_id,
       updated_at         = now()
 WHERE (t.root_location_id, t.root_location_type, t.system_id)
    IS DISTINCT FROM (EXCLUDED.root_location_id, EXCLUDED.root_location_type, EXCLUDED.system_id)
RETURNING *;

-- name: ListAssetsByRootLocation :many
SELECT al.* FROM app.asset_location al
 WHERE al.owner_kind = $1 AND al.owner_id = $2 AND al.root_location_id = $3
 ORDER BY al.item_id;
