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
--
-- ── PHASE 14 ────────────────────────────────────────────────────────────
-- Phase 14 completes the catalogue. Two things above need correcting in
-- light of what this phase found, neither of them a change of plan:
--
-- 1. THIS FILE SEEDS 53, NOT 54 — and the shortfall is a reported
--    specification defect, not a scoping decision. §4.4's own per-domain
--    breakdown (Structures 22 incl. 5 Skyhook, Characters 7, platform 7,
--    Wars 6, Corporations 5, Sovereignty 4, Contracts 1, Alliances 1) sums
--    to 53, while the same sentence — and the roadmap, and this file's own
--    Phase 1a header above, and migration 00008's — states 54.
--
--    The total is the side that is VERIFIED: docs/BASELINE.md §4 records
--    Phase 0 measuring 54 concrete types against a real clone of
--    eveseat/notifications at a pinned commit. So one of the eight domain
--    counts is understated by one, and which one cannot be determined from
--    this environment (BASELINE.md recorded only the total; the upstream is
--    not fetchable here). Seeding a guessed 54th row into a domain that may
--    not be the short one would replace a documented shortfall with a
--    silent, wrong assignment. Full reasoning, and the one command that
--    settles it against a clone, are on catalogue.DocumentedTotal in
--    internal/alerting/catalogue/domains.go.
--
-- 2. THE THRESHOLD ROWS STILL CANNOT BE SEEDED UNCONDITIONALLY, and the
--    Phase 1a note above explains exactly why: app.esi_route may still be
--    empty when seeds run. `hangar migrate up` applies seeds (db/seed.go)
--    before anything ingests the spec, so the four threshold rows below
--    are inserted through a JOIN against app.esi_route — present once the
--    catalogue has been ingested, a silent no-op before then. ApplySeeds
--    is idempotent and runs on EVERY `migrate up`, so the rows complete
--    themselves on the first run after ingest; nothing is lost, and a
--    fabricated route_id is never written. An installation that wants
--    them immediately can re-run `hangar migrate up` after boot.
--
-- The Go catalogue (internal/alerting/catalogue) is the build-time source
-- of truth; TestSeedSQLMatchesGoCatalogue asserts this file and it contain
-- exactly the same alert types, so the two cannot drift.
--
-- Type-name provenance: every 'esi_notification' name below appears
-- verbatim in the live ingested spec's own notification `type` enum
-- (internal/esi/catalogue/embedded/openapi.snapshot.json), machine-checked
-- by TestCatalogueTypesExistInLiveSpecEnum. Which types are promoted to
-- alerts, and their domain assignment, is HANGAR's judgement constrained
-- to §4.4's counts — eveseat/notifications, the nominal upstream for that
-- selection, was not reachable from the build environment. See seed.go's
-- sourcing note.
--
-- One CCP spec quirk deliberately NOT reproduced here: the live enum spells
-- WarAdopted with a trailing space ("WarAdopted "). The trimmed name is
-- stored, and internal/alerting/catalogue.Normalize trims incoming payload
-- types before lookup, so an operator never has to type an invisible
-- character into a routing rule.

-- ── Non-threshold rows (49): 'esi_notification' + 'domain_event' ─────────
INSERT INTO app.alert_type (alert_type, domain, category, default_enabled) VALUES
    -- platform (7) — HANGAR's own domain events (seeded since Phase 1a).
    ('hangar.platform.replica_clustered',        'platform', 'domain_event', true),
    ('hangar.platform.replica_solo',              'platform', 'domain_event', true),
    ('hangar.platform.esi_pin_advanced',          'platform', 'domain_event', true),
    ('hangar.platform.error_budget_420',          'platform', 'domain_event', true),
    ('hangar.platform.sde_import_failed',         'platform', 'domain_event', true),
    ('hangar.provisioning.revocation_exposed',    'platform', 'domain_event', true),
    ('hangar.provisioning.driver_unreachable',    'platform', 'domain_event', true),

    -- structures (20 of 22 — the two threshold rows follow below)
    ('StructureUnderAttack',                     'structures', 'esi_notification', true),
    ('StructureLostShields',                     'structures', 'esi_notification', true),
    ('StructureLostArmor',                       'structures', 'esi_notification', true),
    ('StructureDestroyed',                       'structures', 'esi_notification', true),
    ('StructureFuelAlert',                       'structures', 'esi_notification', true),
    ('StructureAnchoring',                       'structures', 'esi_notification', true),
    ('StructureUnanchoring',                     'structures', 'esi_notification', true),
    ('StructureOnline',                          'structures', 'esi_notification', true),
    ('StructureServicesOffline',                 'structures', 'esi_notification', true),
    ('StructureWentHighPower',                   'structures', 'esi_notification', true),
    ('StructureWentLowPower',                    'structures', 'esi_notification', true),
    ('StructureImpendingAbandonmentAssetsAtRisk','structures', 'esi_notification', true),
    ('OwnershipTransferred',                     'structures', 'esi_notification', true),
    ('TowerAlertMsg',                            'structures', 'esi_notification', true),
    ('TowerResourceAlertMsg',                    'structures', 'esi_notification', true),
    -- the 5 Skyhook types §4.4 names explicitly
    ('SkyhookUnderAttack',                       'structures', 'esi_notification', true),
    ('SkyhookLostShields',                       'structures', 'esi_notification', true),
    ('SkyhookDestroyed',                         'structures', 'esi_notification', true),
    ('SkyhookDeployed',                          'structures', 'esi_notification', true),
    ('SkyhookOnline',                            'structures', 'esi_notification', true),

    -- characters (7)
    ('CharTerminationMsg',                       'characters', 'esi_notification', true),
    ('CharLeftCorpMsg',                          'characters', 'esi_notification', true),
    ('CharMedalMsg',                             'characters', 'esi_notification', true),
    ('CloneActivationMsg2',                      'characters', 'esi_notification', true),
    ('JumpCloneDeletedMsg1',                     'characters', 'esi_notification', true),
    ('InsurancePayoutMsg',                       'characters', 'esi_notification', false),
    ('ExpertSystemExpiryImminent',               'characters', 'esi_notification', false),

    -- wars (6) — notification-derived, per §4.4 [v3.1 — B10]. No wars
    -- endpoint and no wars table exist, and none is invented.
    ('WarDeclared',                              'wars', 'esi_notification', true),
    ('WarInvalid',                               'wars', 'esi_notification', true),
    ('WarRetractedByConcord',                    'wars', 'esi_notification', true),
    ('WarAdopted',                               'wars', 'esi_notification', true),
    ('WarInherited',                             'wars', 'esi_notification', true),
    ('AllyJoinedWarDefenderMsg',                 'wars', 'esi_notification', true),

    -- corporations (4 of 5 — the extraction threshold follows below)
    ('CorpAppNewMsg',                            'corporations', 'esi_notification', true),
    ('CharAppAcceptMsg',                         'corporations', 'esi_notification', true),
    ('CharAppWithdrawMsg',                       'corporations', 'esi_notification', true),
    ('CorpNewCEOMsg',                            'corporations', 'esi_notification', true),

    -- sovereignty (4)
    ('SovStructureReinforced',                   'sovereignty', 'esi_notification', true),
    ('SovStructureDestroyed',                    'sovereignty', 'esi_notification', true),
    ('EntosisCaptureStarted',                    'sovereignty', 'esi_notification', true),
    ('SovCommandNodeEventStarted',               'sovereignty', 'esi_notification', true),

    -- alliances (1)
    ('AllianceCapitalChanged',                   'alliances', 'esi_notification', true)
ON CONFLICT (alert_type) DO UPDATE
   SET domain          = EXCLUDED.domain,
       category        = EXCLUDED.category,
       default_enabled = EXCLUDED.default_enabled;

-- ── Threshold rows (4) ──────────────────────────────────────────────────
-- Each declares its source route by (method, upstream_path) — verbatim
-- from the live spec, Principle 5 — and the JOIN resolves it to the FK.
-- No route ingested yet ⇒ no rows inserted, and no fabricated FK.
--
-- Note two deliberate spellings that look like typos and are not:
--   * the starbase source is the DETAIL route (.../starbases/{starbase_id}),
--     because app.starbase_detail.fuels — the actual fuel bay — is only
--     populated by the detail fan-out, never by the list route;
--   * the mining extractions path is SINGULAR (/corporation/...), which is
--     how the live spec spells it.
INSERT INTO app.alert_type (alert_type, domain, category, source_route_id, default_enabled)
SELECT v.alert_type, v.domain, 'threshold', r.route_id, v.default_enabled
  FROM (VALUES
        ('corporation.structure.fuel_low',  'structures',   'GET', '/corporations/{corporation_id}/structures',                true),
        ('corporation.starbase.fuel_low',   'structures',   'GET', '/corporations/{corporation_id}/starbases/{starbase_id}',   true),
        ('corporation.moon_extraction.due', 'corporations', 'GET', '/corporation/{corporation_id}/mining/extractions',         true),
        ('corporation.contract.expiring',   'contracts',    'GET', '/corporations/{corporation_id}/contracts',                 true)
       ) AS v(alert_type, domain, method, upstream_path, default_enabled)
  JOIN app.esi_route r
    ON r.method = v.method
   AND r.upstream_path = v.upstream_path
   AND r.retired_at IS NULL
ON CONFLICT (alert_type) DO UPDATE
   SET domain          = EXCLUDED.domain,
       category        = EXCLUDED.category,
       source_route_id = EXCLUDED.source_route_id,
       default_enabled = EXCLUDED.default_enabled;
