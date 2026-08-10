-- Project HANGAR — Phase 8.1: skyhook & sovereignty-hub reagent fixup.
--
-- SPEC DEFECT, discovered auditing the Phase 8 handoff before Phase 9:
-- `app.corporation_skyhook.fuel_expires` and
-- `app.corporation_sovereignty_hub.fuel_expires` (00010's tables #15-#16)
-- copy corporation_structure/corporation_starbase's "fuel that expires"
-- concept onto two structure types that do not use it. Neither
-- GET /corporations/{id}/structures/skyhooks/{skyhook_id} nor
-- .../sovereignty-hubs/{sovereignty_hub_id} (the live embedded spec,
-- internal/esi/catalogue/embedded/openapi.snapshot.json) returns anything
-- resembling a fuel expiry timestamp anywhere in either response. Both
-- structures are powered by a REAGENT BAY instead:
--   skyhook detail:          reagents: [{type_id, secured_stock, unsecured_stock, last_cycle}]
--   sovereignty-hub detail:  reagent_bay: {last_updated, reagents: [...]}
-- fuel_expires would sit permanently NULL forever under any real sync —
-- worse than merely absent, it actively implies a capability (a fuel-low
-- alert keyed on this column) that can never fire. Dropped outright and
-- replaced with `reagents jsonb`, mirroring `starbase_detail.fuels`'
-- existing jsonb-array-of-line-items shape.
--
-- SECOND DEFECT, same audit: `corporation_skyhook.type_id` and
-- `corporation_skyhook.system_id`, and `corporation_sovereignty_hub.type_id`,
-- were all migrated NOT NULL, but NONE of the three is obtainable from ESI
-- pre-SDE:
--   - type_id: both structure kinds have exactly one real type in EVE, but
--     neither the list nor the detail response ever echoes it (sovereignty
--     hub detail's `upgrades[].type_id` is a different concept — the
--     modules fitted to the hub, not the hub's own type). Hardcoding a
--     "known" numeric type id here without a verifiable source (the SDE
--     type catalogue, Phase 9/25) would be exactly the kind of silent
--     guess Principle 13 exists to prevent.
--   - corporation_skyhook.system_id: the list endpoint gives only
--     `planet_id`; resolving planet -> system needs the SDE join
--     (`sde.mapDenormalize` equivalent), also Phase 9/25.
-- (corporation_sovereignty_hub.system_id has NO such problem — the list
-- endpoint's `solar_system_id` is direct — so it stays NOT NULL.)
--
-- Both genuinely unresolvable columns are relaxed to nullable rather than
-- worked around with a guessed constant; absent ≠ wrong, and a NULL here
-- reads honestly as "not yet resolvable" until Phase 9/25's SDE import
-- exists to fill it in.
--
-- +goose Up

ALTER TABLE app.corporation_skyhook
    ALTER COLUMN type_id DROP NOT NULL,
    ALTER COLUMN system_id DROP NOT NULL,
    DROP COLUMN fuel_expires,
    ADD COLUMN reagents jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN is_active boolean;

ALTER TABLE app.corporation_sovereignty_hub
    ALTER COLUMN type_id DROP NOT NULL,
    DROP COLUMN fuel_expires,
    ADD COLUMN reagents jsonb NOT NULL DEFAULT '[]';

-- +goose Down

ALTER TABLE app.corporation_sovereignty_hub
    DROP COLUMN reagents,
    ADD COLUMN fuel_expires timestamptz,
    ALTER COLUMN type_id SET NOT NULL;

ALTER TABLE app.corporation_skyhook
    DROP COLUMN is_active,
    DROP COLUMN reagents,
    ADD COLUMN fuel_expires timestamptz,
    ALTER COLUMN system_id SET NOT NULL,
    ALTER COLUMN type_id SET NOT NULL;
