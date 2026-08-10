-- Project HANGAR — Phase 11: entitlement_rule.source_kind fixup.
--
-- SPEC DEFECT, discovered while implementing the entitlement engine:
-- 00007_platform_provisioning.sql's CHECK constraint on
-- app.entitlement_rule.source_kind encodes
--   ('role', 'squad', 'corporation', 'alliance', 'permission', 'user', 'manual')
-- but 01_ARCHITECTURE.md §9.1 and 00_SRS_v3.1.md's entitlement-engine
-- paragraph both independently give the SAME seven grant sources, worded
-- identically: "user, role, corporation, alliance, corp title, squad,
-- public." Two independent docs agreeing on one list, against a migration
-- that has room for exactly seven values but the WRONG two of them, is the
-- Phase 1a-era defect — not a case (like Phase 10's) where the doc
-- referenced a split the schema never had room for at all.
--
-- `permission` and `manual` are replaced with `corp_title` and `public`:
--   - `corp_title` is exactly what app.corporation_title /
--     app.corporation_member_title (Phase 8, 00010_domain_corporation_
--     structure.sql) already exist to support. source_ref encodes
--     "corporation_id:title_id" — title_id alone is only unique within
--     one corporation (00010's PRIMARY KEY is (corporation_id, title_id)).
--   - `public` is the trivial "matches every user" source; source_ref is
--     unused (empty string) for this kind.
-- `permission` (an entitlement keyed off an RBAC permission) and `manual`
-- (a source_kind that would duplicate what the `user` kind + admin UI
-- already cover) appear nowhere else in the codebase outside this
-- constraint — grepped across db/queries, internal/, and both docs before
-- writing this migration — so neither is load-bearing. No rows exist in
-- app.entitlement_rule outside tests (the table has had no writer since
-- Phase 1a stubbed the queries), so there is no data migration to perform.
--
-- +goose Up

ALTER TABLE app.entitlement_rule
    DROP CONSTRAINT entitlement_rule_source_kind_check,
    ADD CONSTRAINT entitlement_rule_source_kind_check
        CHECK (source_kind IN ('role', 'squad', 'corporation', 'alliance', 'corp_title', 'user', 'public'));

-- +goose Down

ALTER TABLE app.entitlement_rule
    DROP CONSTRAINT entitlement_rule_source_kind_check,
    ADD CONSTRAINT entitlement_rule_source_kind_check
        CHECK (source_kind IN ('role', 'squad', 'corporation', 'alliance', 'permission', 'user', 'manual'));
