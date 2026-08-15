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
-- ── PHASE 14 / 14.1 ─────────────────────────────────────────────────────
-- Phase 14 completed the catalogue and reported a specification defect:
-- §4.4's eight per-domain counts summed to 53 while the same sentence said
-- 54, and eveseat/notifications was not reachable from that build
-- environment to settle which side was wrong. It shipped 53 with the
-- per-domain counts exact and the defect documented.
--
-- PHASE 14.1 measured the upstream directly and resolved it. Applying
-- docs/BASELINE.md §4's own recorded pipeline to the same pinned commit
-- reproduces BASELINE's total of 54 and yields the per-domain breakdown
-- BASELINE never recorded: Structures 23 (not §4.4's 22), Characters 7,
-- Seat/platform 7, Wars 6, Corporations 5, Sovereignty 4, Contracts 1,
-- Alliances 1. The TOTAL was right; §4.4's Structures figure was
-- understated by one. This file now seeds 54. The measurement is committed
-- at testdata/upstream/eveseat_notifications_alerts.txt and is read back by
-- TestCatalogueMatchesMeasuredUpstream; the reasoning is on
-- catalogue.DocumentedTotal.
--
-- The threshold rows STILL cannot be seeded unconditionally, and the Phase
-- 1a note above explains exactly why: `hangar migrate up` applies seeds
-- (db/seed.go) before anything ingests the spec, so the four threshold rows
-- below are inserted through a JOIN against app.esi_route — present once the
-- catalogue has been ingested, a silent no-op before then. ApplySeeds is
-- idempotent and runs on EVERY `migrate up`, so the rows complete themselves
-- on the first run after ingest; nothing is lost, and a fabricated route_id
-- is never written.
--
-- ── PHASE 20.4.1: "ON THE FIRST RUN AFTER INGEST" WAS DOING REAL DAMAGE ──
-- Measured on the 20.4 release image against a throwaway Postgres: the
-- first `migrate up` produced 50 alert types and 0 thresholds; a second one,
-- after the ingest had landed 225 routes, produced 54 and 4. So a fresh
-- installation ran with four alert types MISSING until somebody happened to
-- migrate again.
--
-- The consequence is not a crash, which is what made it survive four
-- phases. app.alert_routing_rule has a foreign key to app.alert_type, so an
-- operator on a fresh installation cannot create a routing rule for a
-- threshold alert at all — and the evaluator then reports it as unrouted,
-- skips it, and looks completely healthy while being structurally incapable
-- of ever firing. SRS §4.4's own sentence about a threshold alert that
-- "silently generates zero alerts" describes this exactly.
--
-- Closed by making the ingest complete its own dependency: cmd/hangar's
-- ingestCatalogue re-applies the seed set after a successful ingest (every
-- file here is idempotent by construction, which is what makes that safe),
-- and both `migrate up` and the ingest now SAY how many threshold types
-- exist — naming the four by name when there are none, because "four of
-- your alert types do not exist" is not a thing an operator discovers by
-- reading a table they have never heard of.
--
-- The Go catalogue (internal/alerting/catalogue) is the build-time source
-- of truth; TestSeedSQLMatchesGoCatalogue asserts this file and it contain
-- exactly the same alert types, so the two cannot drift.
--
-- Type-name provenance: every 'esi_notification' name below is checked
-- against TWO independent sources — the live ingested spec's own
-- notification `type` enum (TestCatalogueTypesExistInLiveSpecEnum) and the
-- measured upstream's own set and domain (TestCatalogueMatchesMeasuredUpstream).
--
-- One CCP spec quirk deliberately NOT reproduced here: the live enum spells
-- WarAdopted with a trailing space ("WarAdopted "). That type is not in this
-- catalogue, but internal/alerting/catalogue.Normalize trims incoming payload
-- types before lookup regardless, so no alert type can ever be keyed on an
-- invisible character.

-- ── Non-threshold rows (50): 'esi_notification' + 'domain_event' ────────
INSERT INTO app.alert_type (alert_type, domain, category, default_enabled) VALUES
    -- platform (7) — HANGAR's own domain events (seeded since Phase 1a).
    -- The upstream's Seat/ set is SeAT's equivalent: same count, same role,
    -- a different platform's events.
    ('hangar.platform.replica_clustered',        'platform', 'domain_event', true),
    ('hangar.platform.replica_solo',              'platform', 'domain_event', true),
    ('hangar.platform.esi_pin_advanced',          'platform', 'domain_event', true),
    ('hangar.platform.error_budget_420',          'platform', 'domain_event', true),
    ('hangar.platform.sde_import_failed',         'platform', 'domain_event', true),
    ('hangar.provisioning.revocation_exposed',    'platform', 'domain_event', true),
    ('hangar.provisioning.driver_unreachable',    'platform', 'domain_event', true),

    -- structures (21 of 23 — the two fuel thresholds follow below)
    ('StructureUnderAttack',                     'structures', 'esi_notification', true),
    ('StructureLostShields',                     'structures', 'esi_notification', true),
    ('StructureLostArmor',                       'structures', 'esi_notification', true),
    ('StructureDestroyed',                       'structures', 'esi_notification', true),
    ('StructureAnchoring',                       'structures', 'esi_notification', true),
    ('StructureUnanchoring',                     'structures', 'esi_notification', true),
    ('StructureServicesOffline',                 'structures', 'esi_notification', true),
    ('StructureWentHighPower',                   'structures', 'esi_notification', true),
    ('StructureWentLowPower',                    'structures', 'esi_notification', true),
    ('OwnershipTransferred',                     'structures', 'esi_notification', true),
    ('AllAnchoringMsg',                          'structures', 'esi_notification', true),
    ('TowerAlertMsg',                            'structures', 'esi_notification', true),
    ('OrbitalAttacked',                          'structures', 'esi_notification', true),
    ('OrbitalReinforced',                        'structures', 'esi_notification', true),
    -- CCP's own casing is "Moonmining", not "MoonMining".
    ('MoonminingExtractionStarted',              'structures', 'esi_notification', true),
    ('MoonminingExtractionFinished',             'structures', 'esi_notification', true),
    -- the 5 Skyhook types §4.4 names explicitly
    ('SkyhookUnderAttack',                       'structures', 'esi_notification', true),
    ('SkyhookLostShields',                       'structures', 'esi_notification', true),
    ('SkyhookDestroyed',                         'structures', 'esi_notification', true),
    ('SkyhookDeployed',                          'structures', 'esi_notification', true),
    ('SkyhookOnline',                            'structures', 'esi_notification', true),

    -- characters (7 = 5 CCP + 2 HANGAR domain events, standing in for the
    -- upstream's observer-computed Killmail and NewMailMessage)
    ('RaffleCreated',                            'characters', 'esi_notification', false),
    ('RaffleExpired',                            'characters', 'esi_notification', false),
    ('RaffleFinished',                           'characters', 'esi_notification', false),
    ('ResearchMissionAvailableMsg',              'characters', 'esi_notification', false),
    ('StoryLineMissionAvailableMsg',             'characters', 'esi_notification', false),
    ('character.killmail.received',              'characters', 'domain_event',     false),
    ('character.mail.received',                  'characters', 'domain_event',     false),

    -- wars (6) — notification-derived, per §4.4 [v3.1 — B10]. No wars
    -- endpoint and no wars table exist in HANGAR, and none is invented.
    ('WarDeclared',                              'wars', 'esi_notification', true),
    ('AllWarDeclaredMsg',                        'wars', 'esi_notification', true),
    ('AllWarInvalidatedMsg',                     'wars', 'esi_notification', true),
    ('AllyJoinedWarAggressorMsg',                'wars', 'esi_notification', true),
    ('AllyJoinedWarAllyMsg',                     'wars', 'esi_notification', true),
    ('AllyJoinedWarDefenderMsg',                 'wars', 'esi_notification', true),

    -- corporations (4 of 5 — the inactive-member threshold follows below)
    ('CorpAppNewMsg',                            'corporations', 'esi_notification', true),
    ('CharLeftCorpMsg',                          'corporations', 'esi_notification', true),
    ('CorpAllBillMsg',                           'corporations', 'esi_notification', true),
    ('BillPaidCorpAllMsg',                       'corporations', 'esi_notification', true),

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
--   * `membertracking` is one word in the live spec.
INSERT INTO app.alert_type (alert_type, domain, category, source_route_id, default_enabled)
SELECT v.alert_type, v.domain, 'threshold', r.route_id, v.default_enabled
  FROM (VALUES
        ('corporation.structure.fuel_low', 'structures',   'GET', '/corporations/{corporation_id}/structures',              true),
        ('corporation.starbase.fuel_low',  'structures',   'GET', '/corporations/{corporation_id}/starbases/{starbase_id}', true),
        ('corporation.member.inactive',    'corporations', 'GET', '/corporations/{corporation_id}/membertracking',          false),
        ('corporation.contract.expiring',  'contracts',    'GET', '/corporations/{corporation_id}/contracts',               true)
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
