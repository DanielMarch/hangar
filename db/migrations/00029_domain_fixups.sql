-- Project HANGAR — Phase 1b: fixups discovered while building
-- `hangar admin verify-identifier-types`.
--
-- DEFECT: 02_DATABASE_SCHEMA.md §4.4's own DDL block for app.provisioning_audit
-- (reproduced verbatim in db/migrations/00007_platform_provisioning.sql) types
-- `user_id` as a bare `uuid NOT NULL` with no REFERENCES clause — every other
-- actor-reference uuid column in the platform tier points at app.user(user_id)
-- (see webhook_endpoint.owner_user_id, squad.owner_user_id, session.user_id,
-- ...). Without the FK, `hangar admin verify-identifier-types` cannot tell
-- this column apart from a genuinely CCP-supplied bare uuid identifier that
-- was never registered — it is exactly the shape Principle 13's check exists
-- to catch. Fixed additively here rather than editing 00007 (an
-- already-committed migration is not rewritten; Phase 1a is not
-- re-litigated).

-- +goose Up
ALTER TABLE app.provisioning_audit
    ADD CONSTRAINT fk_provisioning_audit_user
    FOREIGN KEY (user_id) REFERENCES app.user(user_id);

-- +goose Down
ALTER TABLE app.provisioning_audit DROP CONSTRAINT fk_provisioning_audit_user;
