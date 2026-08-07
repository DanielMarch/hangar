-- app.planet_colony, app.planet_colony_detail (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertPlanetColony :one
INSERT INTO app.planet_colony AS t (
    character_id, planet_id, solar_system_id, planet_type, owner_id, last_update, upgrade_level, num_pins
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (character_id, planet_id) DO UPDATE
   SET last_update = EXCLUDED.last_update, upgrade_level = EXCLUDED.upgrade_level,
       num_pins = EXCLUDED.num_pins, updated_at = now()
 WHERE (t.last_update, t.upgrade_level, t.num_pins)
    IS DISTINCT FROM (EXCLUDED.last_update, EXCLUDED.upgrade_level, EXCLUDED.num_pins)
RETURNING *;

-- name: ListPlanetColonies :many
SELECT * FROM app.planet_colony WHERE character_id = $1 ORDER BY planet_id;

-- name: UpsertPlanetColonyDetail :one
INSERT INTO app.planet_colony_detail AS t (character_id, planet_id, pins, links, routes)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (character_id, planet_id) DO UPDATE
   SET pins = EXCLUDED.pins, links = EXCLUDED.links, routes = EXCLUDED.routes, updated_at = now()
 WHERE (t.pins, t.links, t.routes) IS DISTINCT FROM (EXCLUDED.pins, EXCLUDED.links, EXCLUDED.routes)
RETURNING *;

-- name: GetPlanetColonyDetail :one
SELECT * FROM app.planet_colony_detail WHERE character_id = $1 AND planet_id = $2;
