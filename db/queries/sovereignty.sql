-- app.sovereignty_campaign, app.sovereignty_system (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertSovereigntyCampaign :one
INSERT INTO app.sovereignty_campaign AS t (
    campaign_id, constellation_id, solar_system_id, structure_id, defender_id, event_type,
    start_time, attackers_score, defender_score
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (campaign_id) DO UPDATE
   SET attackers_score = EXCLUDED.attackers_score, defender_score = EXCLUDED.defender_score, updated_at = now()
 WHERE (t.attackers_score, t.defender_score) IS DISTINCT FROM (EXCLUDED.attackers_score, EXCLUDED.defender_score)
RETURNING *;

-- name: ListSovereigntyCampaigns :many
SELECT * FROM app.sovereignty_campaign ORDER BY start_time DESC;

-- name: DeleteSovereigntyCampaignsNotIn :exec
-- A campaign that concluded simply vanishes from the live feed; there is no
-- "resolved" flag upstream to soft-delete against.
DELETE FROM app.sovereignty_campaign WHERE NOT (campaign_id = ANY(sqlc.arg(keep_campaign_ids)::bigint[]));

-- name: UpsertSovereigntySystem :one
INSERT INTO app.sovereignty_system AS t (system_id, alliance_id, corporation_id, faction_id)
VALUES ($1,$2,$3,$4)
ON CONFLICT (system_id) DO UPDATE
   SET alliance_id = EXCLUDED.alliance_id, corporation_id = EXCLUDED.corporation_id,
       faction_id = EXCLUDED.faction_id, updated_at = now()
 WHERE (t.alliance_id, t.corporation_id, t.faction_id)
    IS DISTINCT FROM (EXCLUDED.alliance_id, EXCLUDED.corporation_id, EXCLUDED.faction_id)
RETURNING *;

-- name: ListSovereigntySystems :many
SELECT * FROM app.sovereignty_system ORDER BY system_id;
