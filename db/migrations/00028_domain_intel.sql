-- Project HANGAR — Phase 1b: intel.
-- 02_DATABASE_SCHEMA.md §5.2 "Intel (1)". GET /characters/{id}/intel:
-- a derived interaction graph over mail, contacts, killmails and standings.
-- Materialised as directed weighted edges between characters, rebuilt by a
-- later-phase job — Phase 1b ships the storage shape only.

-- +goose Up

CREATE TABLE app.character_intel_edge (
    source_character_id bigint      NOT NULL REFERENCES app.character(character_id),
    target_character_id bigint      NOT NULL,
    edge_kind            text        NOT NULL,   -- open vocabulary: 'mail'|'contact'|'killmail'|'standing'
    weight                 double precision NOT NULL DEFAULT 0,   -- NOT money
    last_observed_at          timestamptz NOT NULL,
    updated_at                   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_character_id, target_character_id, edge_kind)
);
CREATE INDEX ON app.character_intel_edge (target_character_id);

-- +goose Down
DROP TABLE app.character_intel_edge;
