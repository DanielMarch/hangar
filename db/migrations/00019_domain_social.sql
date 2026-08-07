-- Project HANGAR — Phase 1b: social.
-- 02_DATABASE_SCHEMA.md §5.2 "Social (5)". `contact`, `contact_label` and
-- `standing` are owner-polymorphic across characters, corporations AND
-- alliances (SRS §6.2-§6.4 all expose contacts/standings), so
-- domain.OwnerKind gains a third value here. `medal` is a corporation-owned
-- definition; `medal_issued` is the corp->character issuance record that
-- also answers /characters/{id}/medals — neither needs owner polymorphism,
-- the relationship is inherently one-directional.

-- +goose Up

-- #1
CREATE TABLE app.contact (
    owner_kind    text    NOT NULL,   -- 'character' | 'corporation' | 'alliance'
    owner_id      bigint  NOT NULL,
    contact_id    bigint  NOT NULL,
    contact_type  text    NOT NULL,   -- open vocabulary: 'character'|'corporation'|'alliance'|'faction'
    standing      double precision NOT NULL,   -- NOT money
    is_blocked    boolean,
    is_watched    boolean,
    label_ids     bigint[] NOT NULL DEFAULT '{}',
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, contact_id)
);

-- #2
CREATE TABLE app.contact_label (
    owner_kind text   NOT NULL,
    owner_id   bigint NOT NULL,
    label_id   bigint NOT NULL,
    name       text   NOT NULL,
    PRIMARY KEY (owner_kind, owner_id, label_id)
);

-- #3 — /characters|corporations/{id}/standings (NPC corp/faction standings;
-- distinct from `contact`, which is player-to-player/corp/alliance).
CREATE TABLE app.standing (
    owner_kind    text    NOT NULL,
    owner_id      bigint  NOT NULL,
    from_id       bigint  NOT NULL,
    from_type     text    NOT NULL,   -- open vocabulary: 'npc_corp'|'faction'|'agent'
    standing      double precision NOT NULL,   -- NOT money
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, from_id)
);

-- #4 — corporation-owned medal definitions.
CREATE TABLE app.medal (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    medal_id       bigint      NOT NULL,
    title          text        NOT NULL,
    description    text,
    created_at     timestamptz,
    creator_id     bigint,
    PRIMARY KEY (corporation_id, medal_id)
);

-- #5 — corp -> character issuance; answers both
-- /corporations/{id}/medals/issued and /characters/{id}/medals.
CREATE TABLE app.medal_issued (
    corporation_id bigint      NOT NULL,
    medal_id       bigint      NOT NULL,
    character_id   bigint      NOT NULL REFERENCES app.character(character_id),
    reason         text,
    status         text,          -- open vocabulary: 'private'|'public'
    issuer_id      bigint      NOT NULL,
    issued_at      timestamptz NOT NULL,
    PRIMARY KEY (corporation_id, medal_id, character_id, issued_at),
    FOREIGN KEY (corporation_id, medal_id) REFERENCES app.medal (corporation_id, medal_id) ON DELETE CASCADE
);
CREATE INDEX ON app.medal_issued (character_id);

-- +goose Down
DROP TABLE app.medal_issued;
DROP TABLE app.medal;
DROP TABLE app.standing;
DROP TABLE app.contact_label;
DROP TABLE app.contact;
