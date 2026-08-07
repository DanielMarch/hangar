-- Project HANGAR — Phase 1a: identity and access.
-- 02_DATABASE_SCHEMA.md §4.1 (#1-#10).
--
-- app.user and app.character have a mutual reference (a user's chosen main
-- character, a character's owning user). app.user is created without the
-- main_character_id FK, then the FK is added once app.character exists —
-- same pattern as app.corporation / app.alliance in 00003.

-- +goose Up

-- #1
CREATE TABLE app.user (
    user_id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    main_character_id bigint,     -- FK added below, after app.character exists
    display_name      text        NOT NULL,
    is_active          boolean    NOT NULL DEFAULT true,
    is_admin           boolean    NOT NULL DEFAULT false,
    last_login_at      timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- #2
CREATE TABLE app.character (
    character_id    bigint      PRIMARY KEY,   -- ESI-typed int64 identifier
    user_id         uuid        REFERENCES app.user(user_id),
    name            text        NOT NULL,
    corporation_id  bigint      REFERENCES app.corporation(corporation_id),
    alliance_id     bigint      REFERENCES app.alliance(alliance_id),
    faction_id      integer,
    security_status double precision,
    birthday        timestamptz,
    gender          text,
    race_id         integer,
    bloodline_id    integer,
    ancestry_id     integer,
    description     text,
    title           text,
    owner_hash      text        NOT NULL,     -- change ⇒ character transferred ⇒ revoke (see character_token)
    deleted_at      timestamptz,              -- soft delete: biomassed / no longer tracked, never DELETE
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.character (user_id);
CREATE INDEX ON app.character (corporation_id);
CREATE INDEX ON app.character (alliance_id);

ALTER TABLE app.user
    ADD CONSTRAINT fk_user_main_character
    FOREIGN KEY (main_character_id) REFERENCES app.character(character_id);

-- #3 — verbatim from 02_DATABASE_SCHEMA.md §4.1.
CREATE TABLE app.character_token (
    character_id       bigint      PRIMARY KEY REFERENCES app.character(character_id),
    key_version        int         NOT NULL,
    wrapped_dek        bytea       NOT NULL,
    nonce              bytea       NOT NULL,
    ciphertext         bytea       NOT NULL,   -- AES-256-GCM(refresh_token)
    -- AAD = character_id ‖ key_version ‖ 'refresh_token'
    access_expires_at  timestamptz,
    valid              boolean     NOT NULL DEFAULT true,
    invalid_reason     text,                   -- open vocabulary, never an ENUM
    owner_hash         text        NOT NULL,   -- change ⇒ character transferred ⇒ revoke
    last_refreshed_at  timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.character_token (valid) WHERE NOT valid;   -- Strict Mode NOT EXISTS probe

-- #4
CREATE TABLE app.character_token_scope (
    character_id bigint NOT NULL REFERENCES app.character_token(character_id) ON DELETE CASCADE,
    scope        text   NOT NULL REFERENCES app.esi_scope(scope),
    PRIMARY KEY (character_id, scope)
);
CREATE INDEX ON app.character_token_scope (scope);

-- #5
CREATE TABLE app.session (
    session_id    uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id       uuid        REFERENCES app.user(user_id) ON DELETE CASCADE,
    pkce_verifier text,
    state         text,
    ip_address    inet,
    user_agent    text,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.session (user_id);
CREATE INDEX ON app.session (expires_at);

-- #6
CREATE TABLE app.api_token (
    token_id      uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id       uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    name          text        NOT NULL,
    hashed_secret bytea       NOT NULL,
    permissions   text[]      NOT NULL DEFAULT '{}',   -- app.permission values (HANGAR closed set)
    last_used_at  timestamptz,
    expires_at    timestamptz,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.api_token (user_id);
CREATE UNIQUE INDEX ON app.api_token (hashed_secret);

-- #7
CREATE TABLE app.api_token_access_log (
    log_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    token_id   uuid        NOT NULL REFERENCES app.api_token(token_id) ON DELETE CASCADE,
    route      text        NOT NULL,
    status     smallint    NOT NULL,
    ip_address inet,
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.api_token_access_log (token_id, at DESC);

-- #8
CREATE TABLE app.share_link (
    link_id    uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    view       text        NOT NULL,   -- HANGAR-defined view/route slug, not an external vocabulary
    params     jsonb       NOT NULL DEFAULT '{}',
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.share_link (user_id);

-- #9 — append-only: every search, every admin action, every lockdown.
CREATE TABLE app.security_log (
    log_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid        REFERENCES app.user(user_id),
    action     text        NOT NULL,   -- open vocabulary: 'search'|'admin.lockdown'|...
    target     text,
    ip_address inet,
    detail     jsonb       NOT NULL DEFAULT '{}',
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.security_log (user_id, at DESC);
CREATE INDEX ON app.security_log (at DESC);

-- #10 — runtime settings including the compatibility pin and the cached
-- JWKS, e.g. key='esi.compatibility_pin', key='sso.jwks_cache'.
CREATE TABLE app.setting (
    key        text        PRIMARY KEY,
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid        REFERENCES app.user(user_id)
);

-- +goose Down
DROP TABLE app.setting;
DROP TABLE app.security_log;
DROP TABLE app.share_link;
DROP TABLE app.api_token_access_log;
DROP TABLE app.api_token;
DROP TABLE app.session;
DROP TABLE app.character_token_scope;
DROP TABLE app.character_token;
ALTER TABLE app.user DROP CONSTRAINT fk_user_main_character;
DROP TABLE app.character;
DROP TABLE app.user;
