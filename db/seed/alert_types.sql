-- Project HANGAR — alert type catalogue (02_DATABASE_SCHEMA.md §4.5 #38).
--
-- SCOPE NOTE (not a defect — a genuine forward dependency). The full
-- catalogue is 54 rows across eight domains (SRS v3.1 §4.4;
-- 03_IMPLEMENTATION_ROADMAP.md Phase 14, which owns
-- internal/alerting/catalogue/{seed,domains,thresholds}.go and this same
-- file path, and whose TestAlertCatalogueSeeds54AcrossEightDomains asserts
-- the exact per-domain counts). Every `category = 'threshold'` row requires
-- a NOT NULL source_route_id FK into app.esi_route — and app.esi_route is
-- empty until Phase 2 ingests the OpenAPI spec. Seeding a threshold row now
-- would mean either a fabricated route_id (breaks the FK's meaning) or a
-- deferred/nullable constraint that weakens the very check Phase 14 relies
-- on. Phase 1a therefore seeds only the non-threshold rows below —
-- 'esi_notification' and 'domain_event' categories, which have no such
-- dependency — as a working, idempotent scaffold. Phase 14 adds the
-- remaining threshold rows and is the phase whose exit criteria assert the
-- total of 54.
--
-- None of Phase 1a's seven named exit tests count rows in this table.

INSERT INTO app.alert_type (alert_type, domain, category, default_enabled) VALUES
    ('hangar.platform.replica_clustered',        'platform', 'domain_event', true),
    ('hangar.platform.replica_solo',              'platform', 'domain_event', true),
    ('hangar.platform.esi_pin_advanced',          'platform', 'domain_event', true),
    ('hangar.platform.error_budget_420',          'platform', 'domain_event', true),
    ('hangar.platform.sde_import_failed',         'platform', 'domain_event', true),
    ('hangar.provisioning.revocation_exposed',    'platform', 'domain_event', true),
    ('hangar.provisioning.driver_unreachable',    'platform', 'domain_event', true)
ON CONFLICT (alert_type) DO UPDATE
   SET domain          = EXCLUDED.domain,
       category        = EXCLUDED.category,
       default_enabled = EXCLUDED.default_enabled;
