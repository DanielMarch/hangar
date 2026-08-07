-- Project HANGAR — Phase 1b: tools.
-- 02_DATABASE_SCHEMA.md §5.2 "Tools (3)". `character_note` is an
-- administrator note about a character (character-scoped, HANGAR-owned
-- content — not synced from ESI). `insurance_price` and `moon_report` are
-- global/computed support tables behind §6.7's utility routes.

-- +goose Up

-- #1 — POST /tools/character/{id}/notes.
CREATE TABLE app.character_note (
    note_id      uuid        PRIMARY KEY DEFAULT uuidv7(),
    character_id bigint      NOT NULL REFERENCES app.character(character_id),
    author_user_id uuid      NOT NULL REFERENCES app.user(user_id),
    body            text        NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.character_note (character_id, created_at DESC);

-- #2 — GET /tools/insurance: per-type insurance levels and prices.
CREATE TABLE app.insurance_price (
    type_id integer NOT NULL,
    level   text    NOT NULL,   -- open vocabulary: 'basic'|'standard'|'gold'|'platinum'
    cost    numeric(30,2) NOT NULL,   -- money
    payout  numeric(30,2) NOT NULL,   -- money
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (type_id, level)
);

-- #3 — POST /tools/moon-report/parse: parsed moon-scan clipboard output.
CREATE TABLE app.moon_report (
    report_id    uuid        PRIMARY KEY DEFAULT uuidv7(),
    submitted_by uuid        NOT NULL REFERENCES app.user(user_id),
    moon_id      bigint,
    raw_text     text        NOT NULL,
    parsed       jsonb       NOT NULL DEFAULT '[]',   -- [{type_id, percentage}]
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app.moon_report;
DROP TABLE app.insurance_price;
DROP TABLE app.character_note;
