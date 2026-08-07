-- Project HANGAR — Phase 1b: corporation projects.
-- 02_DATABASE_SCHEMA.md §5.2 "Corporation projects (3, UUID-keyed)" and §5.3
-- ("Corporation projects — UUID-keyed, no coercion", Principle 13 / Gate 6
-- fixture). project_id is issued by CCP as `type: string, format: uuid` —
-- it is NEVER generated here (no DEFAULT) and NEVER stored as text.

-- +goose Up

-- #1 — verbatim from 02_DATABASE_SCHEMA.md §5.3.
CREATE TABLE app.corporation_project (
    project_id      uuid        PRIMARY KEY,           -- FROM CCP. type: string, format: uuid.
                                                        -- NOT generated here; NOT stored as text.
    corporation_id  bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    name            text        NOT NULL,
    state           text        NOT NULL,              -- open vocabulary
    contribution_type text,
    target_progress numeric(30,2),
    current_progress numeric(30,2),
    reward_isk      numeric(30,2),                     -- money
    expires_at      timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.corporation_project (corporation_id);

-- #2 — GET /corporations/{id}/projects/{project_id}/contributors: the
-- roster of characters participating, distinct from the amounts in #3.
CREATE TABLE app.corporation_project_contributor (
    project_id   uuid    NOT NULL REFERENCES app.corporation_project(project_id),
    character_id bigint  NOT NULL,                     -- int64 identifier, alongside a uuid one
    joined_at    timestamptz,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, character_id)
);

-- #3 — verbatim from 02_DATABASE_SCHEMA.md §5.3. The Gate 6 / Phase 9
-- fixture proving a uuid PK joins against a bigint FK without coercion.
CREATE TABLE app.corporation_project_contribution (
    project_id   uuid    NOT NULL REFERENCES app.corporation_project(project_id),
    character_id bigint  NOT NULL,                     -- int64 identifier, alongside a uuid one
    amount       numeric(30,2) NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, character_id)
);

-- +goose Down
DROP TABLE app.corporation_project_contribution;
DROP TABLE app.corporation_project_contributor;
DROP TABLE app.corporation_project;
