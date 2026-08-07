-- Project HANGAR — Phase 1a: RBAC and squads.
-- 02_DATABASE_SCHEMA.md §4.2 (#11-#20).
--
-- `effect` on app.role_grant and app.entitlement_rule (00007), `type` on
-- app.squad and `status` on app.squad_application are HANGAR-internal closed
-- sets, so a CHECK constraint is acceptable here — Principle 14 scopes
-- openness to *external* vocabularies only (SRS v3.1 defect B11).

-- +goose Up

-- #11
CREATE TABLE app.role (
    role_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    name        text        NOT NULL UNIQUE,
    description text,
    is_system   boolean     NOT NULL DEFAULT false,  -- seeded/built-in roles; not administrator-deletable
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- #12 — HANGAR's own closed set (Principle 14 exception, SRS v3.1 §5.1/§3.3).
-- Rows are seeded from db/seed/permissions.sql, generated from the Go source
-- of truth in internal/domain/vocabulary.go; TestPermissionSeedMatchesGoSet
-- fails CI on divergence.
CREATE TABLE app.permission (
    permission  text PRIMARY KEY,
    description text NOT NULL,
    category    text NOT NULL    -- HANGAR-internal grouping, e.g. 'characters'|'admin'|'squads'
);

-- #13
CREATE TABLE app.role_grant (
    grant_id   uuid        PRIMARY KEY DEFAULT uuidv7(),
    role_id    uuid        NOT NULL REFERENCES app.role(role_id) ON DELETE CASCADE,
    permission text        NOT NULL REFERENCES app.permission(permission),
    effect     text        NOT NULL CHECK (effect IN ('allow', 'deny')),  -- HANGAR-internal 2-value set
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (role_id, permission, effect)
);
CREATE INDEX ON app.role_grant (permission);

-- #14
CREATE TABLE app.user_role (
    user_id    uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    role_id    uuid        NOT NULL REFERENCES app.role(role_id) ON DELETE CASCADE,
    granted_at timestamptz NOT NULL DEFAULT now(),
    granted_by uuid        REFERENCES app.user(user_id),
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX ON app.user_role (role_id);

-- #15 — materialised projection, refreshed on grant change (§4.2).
CREATE TABLE app.effective_permission (
    user_id      uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    permission   text        NOT NULL REFERENCES app.permission(permission),
    permitted    boolean     NOT NULL,
    refreshed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, permission)
);
CREATE INDEX ON app.effective_permission (permission) WHERE permitted;

-- #16
CREATE TABLE app.squad (
    squad_id      uuid        PRIMARY KEY DEFAULT uuidv7(),
    name          text        NOT NULL,
    type          text        NOT NULL DEFAULT 'open' CHECK (type IN ('open', 'managed', 'hidden')),
    owner_user_id uuid        NOT NULL REFERENCES app.user(user_id),
    description   text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.squad (owner_user_id);

-- #17
CREATE TABLE app.squad_member (
    squad_id     uuid        NOT NULL REFERENCES app.squad(squad_id) ON DELETE CASCADE,
    character_id bigint      NOT NULL REFERENCES app.character(character_id) ON DELETE CASCADE,
    joined_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (squad_id, character_id)
);
CREATE INDEX ON app.squad_member (character_id);

-- #18
CREATE TABLE app.squad_moderator (
    squad_id   uuid        NOT NULL REFERENCES app.squad(squad_id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (squad_id, user_id)
);
CREATE INDEX ON app.squad_moderator (user_id);

-- #19 — squad membership grants an RBAC role.
CREATE TABLE app.squad_role (
    squad_id uuid NOT NULL REFERENCES app.squad(squad_id) ON DELETE CASCADE,
    role_id  uuid NOT NULL REFERENCES app.role(role_id) ON DELETE CASCADE,
    PRIMARY KEY (squad_id, role_id)
);
CREATE INDEX ON app.squad_role (role_id);

-- #20
CREATE TABLE app.squad_application (
    application_id uuid        PRIMARY KEY DEFAULT uuidv7(),
    squad_id       uuid        NOT NULL REFERENCES app.squad(squad_id) ON DELETE CASCADE,
    character_id   bigint      NOT NULL REFERENCES app.character(character_id) ON DELETE CASCADE,
    status         text        NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'approved', 'rejected', 'withdrawn')),
    message        text,
    resolved_by    uuid        REFERENCES app.user(user_id),
    resolved_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.squad_application (squad_id) WHERE status = 'pending';
CREATE INDEX ON app.squad_application (character_id);

-- +goose Down
DROP TABLE app.squad_application;
DROP TABLE app.squad_role;
DROP TABLE app.squad_moderator;
DROP TABLE app.squad_member;
DROP TABLE app.squad;
DROP TABLE app.effective_permission;
DROP TABLE app.user_role;
DROP TABLE app.role_grant;
DROP TABLE app.permission;
DROP TABLE app.role;
