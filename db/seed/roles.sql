-- Project HANGAR — built-in RBAC roles (02_DATABASE_SCHEMA.md §4.2 #11).
-- Idempotent by name; role_id keeps its uuidv7() default so re-seeding an
-- already-seeded installation neither regenerates nor duplicates a row.
-- Grants (app.role_grant) are an administrator/Phase-10 concern, not seeded
-- here — a fresh installation's `admin` role is empty of grants until an
-- operator (or a later migration) populates them, which is safer than
-- shipping a silently over-privileged default.

INSERT INTO app.role (name, description, is_system) VALUES
    ('admin',  'Full administrative access to the HANGAR installation', true),
    ('member', 'Baseline role held by every linked user',                true)
ON CONFLICT (name) DO NOTHING;
