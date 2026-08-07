-- Project HANGAR — Phase 1a: schemas.
-- 02_DATABASE_SCHEMA.md §1. `app` is HANGAR-owned; `sde` is HANGAR-owned but
-- populated by `hangar admin sde import` (Phase 9), not Goose DML. `river`
-- and `sde_next` are never touched here: river is migrated by River's own
-- migrator (see cmd/hangar/migrate.go) and sde_next is created/dropped by the
-- SDE importer's atomic swap.

-- +goose Up
CREATE SCHEMA IF NOT EXISTS app;
CREATE SCHEMA IF NOT EXISTS sde;

-- +goose Down
DROP SCHEMA IF EXISTS sde CASCADE;
DROP SCHEMA IF EXISTS app CASCADE;
