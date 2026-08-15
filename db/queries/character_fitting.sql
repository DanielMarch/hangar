-- app.character_fitting, app.character_fitting_item (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertCharacterFitting :one
INSERT INTO app.character_fitting AS t (character_id, fitting_id, name, description, ship_type_id)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (character_id, fitting_id) DO UPDATE
   SET name = EXCLUDED.name, description = EXCLUDED.description, ship_type_id = EXCLUDED.ship_type_id,
       updated_at = now()
 WHERE (t.name, t.description, t.ship_type_id) IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.description, EXCLUDED.ship_type_id)
RETURNING *;

-- name: ListCharacterFittings :many
SELECT * FROM app.character_fitting WHERE character_id = $1 ORDER BY fitting_id;

-- name: DeleteCharacterFittingsNotIn :exec
DELETE FROM app.character_fitting
 WHERE character_id = $1 AND NOT (fitting_id = ANY(sqlc.arg(keep_fitting_ids)::bigint[]));

-- name: UpsertCharacterFittingItem :one
INSERT INTO app.character_fitting_item AS t (character_id, fitting_id, record_id, type_id, flag, quantity)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (character_id, fitting_id, record_id) DO UPDATE
   SET type_id = EXCLUDED.type_id, flag = EXCLUDED.flag, quantity = EXCLUDED.quantity
 WHERE (t.type_id, t.flag, t.quantity) IS DISTINCT FROM (EXCLUDED.type_id, EXCLUDED.flag, EXCLUDED.quantity)
RETURNING *;

-- name: ListCharacterFittingItems :many
SELECT * FROM app.character_fitting_item WHERE character_id = $1 AND fitting_id = $2 ORDER BY record_id;

-- name: DeleteCharacterFittingItemsNotIn :exec
-- PHASE 20.7 (B48). DeleteCharacterFittingsNotIn prunes whole fittings and
-- app.character_fitting_item cascades with them, so a DELETED fitting takes
-- its items along. An EDITED one does not: the fitting_id survives, and
-- without this the modules a pilot removed would stay behind forever.
--
-- That is not a cosmetic staleness. Capability #8's headline feature is the
-- EFT export, which is rendered by walking exactly these rows — a ghost
-- module produces a fitting block that the pilot never saved and that will
-- not fit the hull. Pruning by absence is safe here in a way it is not for
-- reference data (see SyncInsurancePrices' note): this set is the complete
-- item list for ONE fitting, delivered in the same response as the fitting
-- itself, so "absent" is unambiguous.
DELETE FROM app.character_fitting_item
 WHERE character_id = $1 AND fitting_id = $2
   AND NOT (record_id = ANY(sqlc.arg(keep_record_ids)::bigint[]));
