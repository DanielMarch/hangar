-- Project HANGAR — Phase 7: character-core route handlers.
--
-- Two classes of fixup, discovered while building the character-sheet
-- handlers against the actual embedded spec (2026-08-04,
-- internal/esi/catalogue/embedded/openapi.snapshot.json) rather than the
-- roadmap's summary of it. Additive, following 00029's precedent — an
-- already-committed migration is not rewritten.
--
-- 1. PIN COMPLIANCE, CORRECTED. The roadmap said GET /characters/{id}'s
--    `title_id` is "renamed" `corporation_title_id`. The real spec has
--    NEITHER name: the field is `corporation_title` (a plain string — the
--    character's current corp title's display name, not an id), and
--    `character_title_id` is an unrelated new concept — a UUID-keyed
--    "cosmetic title" (`$ref: UUID`), nothing to do with corporation
--    titles at all. `app.character.title` (migrated in 00004, Phase 1a)
--    already holds a free-text title string and is reused as-is for
--    `corporation_title` — same shape, no rename needed. The two genuinely
--    new columns are added here.
--
-- 2. PRINCIPLE 13 DEFECTS. Cross-referencing every "_id"-shaped column
--    Phase 7 writes to against the spec's declared format turned up six
--    bigint/int64 fields Phase 1b had stored as `integer` (int32) —
--    `hangar admin verify-identifier-types` would fail the build on every
--    one of these once real data starts flowing through them. All six are
--    identifiers CCP declares `format: int64`, verbatim from the spec:
--      character_skill.skill_id            (CharactersSkillsSkill)
--      character_skillqueue.skill_id       (CharactersSkillqueueSkill -> TypeID)
--      character_clone.implants            (CharactersCharacterIdClonesGet.jump_clones[].implants[])
--      character_implant.type_id           (CharactersCharacterIdImplantsGet[])
--      character_agent_research.skill_type_id (CharactersCharacterIdAgentsResearchGet)
--      character_location.solar_system_id  (CharactersCharacterIdLocationGet)
--      character_location.ship_type_id     (CharactersCharacterIdShipGet)

-- +goose Up

-- #1 — pin: two genuinely new columns.
ALTER TABLE app.character
    ADD COLUMN character_title_id uuid,               -- cosmetic title; $ref: UUID, unrelated to corp titles
    ADD COLUMN achievement_score  bigint NOT NULL DEFAULT 0;   -- required int64 in the spec, NOT money

-- #2 — Principle 13 fixups.
ALTER TABLE app.character_skill           ALTER COLUMN skill_id      TYPE bigint;
ALTER TABLE app.character_skillqueue      ALTER COLUMN skill_id      TYPE bigint;
ALTER TABLE app.character_clone           ALTER COLUMN implants      TYPE bigint[];
ALTER TABLE app.character_implant         ALTER COLUMN type_id       TYPE bigint;
ALTER TABLE app.character_agent_research  ALTER COLUMN skill_type_id TYPE bigint;
ALTER TABLE app.character_location        ALTER COLUMN solar_system_id TYPE bigint;
ALTER TABLE app.character_location        ALTER COLUMN ship_type_id  TYPE bigint;

-- +goose Down
ALTER TABLE app.character_location        ALTER COLUMN ship_type_id  TYPE integer;
ALTER TABLE app.character_location        ALTER COLUMN solar_system_id TYPE integer;
ALTER TABLE app.character_agent_research  ALTER COLUMN skill_type_id TYPE integer;
ALTER TABLE app.character_implant         ALTER COLUMN type_id       TYPE integer;
ALTER TABLE app.character_clone           ALTER COLUMN implants      TYPE integer[];
ALTER TABLE app.character_skillqueue      ALTER COLUMN skill_id      TYPE integer;
ALTER TABLE app.character_skill           ALTER COLUMN skill_id      TYPE integer;

ALTER TABLE app.character
    DROP COLUMN achievement_score,
    DROP COLUMN character_title_id;
