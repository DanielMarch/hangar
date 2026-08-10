-- Project HANGAR — Phase 8 fix: app.mining_extraction is missing
-- structure_id.
--
-- SPEC DEFECT (02_DATABASE_SCHEMA.md §5.2 / db/migrations/00016, table #4):
-- GET /corporation/{corporation_id}/mining/extractions declares
-- structure_id as a REQUIRED response field (the structure at which the
-- extraction is happening — moon_id alone does not identify it, a moon can
-- have had multiple extractor structures over time) in the live embedded
-- spec (internal/esi/catalogue/embedded/openapi.snapshot.json), but the
-- Phase 1b migration never added a column for it. Dropping a required
-- identifier field would be field loss (01_ARCHITECTURE.md Principle 13);
-- fixed here rather than worked around by discarding the value.
--
-- +goose Up

ALTER TABLE app.mining_extraction ADD COLUMN structure_id bigint NOT NULL DEFAULT 0;
ALTER TABLE app.mining_extraction ALTER COLUMN structure_id DROP DEFAULT;

-- +goose Down
ALTER TABLE app.mining_extraction DROP COLUMN structure_id;
