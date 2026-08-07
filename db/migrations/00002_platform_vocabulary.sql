-- Project HANGAR — Phase 1a: open vocabularies + ESI scope registry.
-- 02_DATABASE_SCHEMA.md §4.7 (#50 app.open_vocabulary) and §4.3 (#22 app.esi_scope).
--
-- These two land first because they have no foreign-key dependencies and
-- because app.character_token_scope (00004) and app.esi_route_scope (00006)
-- both reference app.esi_scope.

-- +goose Up

-- #50 — Principle 14's one mechanism for every open external vocabulary
-- (ref_type, location_type, notification type, scope strings, x-cache-mode
-- values, contract status, …). No ENUM. Unrecognised values are ingested and
-- surfaced, never rejected.
CREATE TABLE app.open_vocabulary (
    vocabulary      text        NOT NULL,   -- 'ref_type'|'location_type'|'notification_type'|
                                            -- 'scope'|'cache_mode'|'contract_status'|'required_role'
    value           text        NOT NULL,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    occurrences     bigint      NOT NULL DEFAULT 1,
    acknowledged_at timestamptz,
    acknowledged_by uuid,
    PRIMARY KEY (vocabulary, value)
);
CREATE INDEX ON app.open_vocabulary (vocabulary) WHERE acknowledged_at IS NULL;

-- #22 — scopes carry route relationships (esi_route_scope) and token
-- relationships (character_token_scope) that a generic open_vocabulary row
-- cannot express, so it is kept as its own table despite being conceptually
-- an open vocabulary too. scope text itself remains opaque: never parsed,
-- never validated against a grammar (SRS v3.1 §4.5, Phase 5 adversarial test).
CREATE TABLE app.esi_scope (
    scope           text        PRIMARY KEY,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    acknowledged_at timestamptz
);
CREATE INDEX ON app.esi_scope (acknowledged_at) WHERE acknowledged_at IS NULL;

-- +goose Down
DROP TABLE app.esi_scope;
DROP TABLE app.open_vocabulary;
