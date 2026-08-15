-- app.corporation_structure / starbase_detail / corporation_member_tracking
-- / contract, read as THRESHOLD SOURCES — Phase 20.4, defect B25.
--
-- 00_SRS_v3.1.md §4.4's third alert category is `threshold`: an alert
-- HANGAR computes for itself by watching synced data cross a boundary,
-- rather than one CCP sends or one HANGAR's own platform raises. The
-- catalogue has declared four of them since Phase 14 —
-- corporation.structure.fuel_low, corporation.starbase.fuel_low,
-- corporation.member.inactive and corporation.contract.expiring — each
-- with the source route §4.4 requires it to declare. Nothing evaluated
-- them, which is why internal/alerting.ThresholdFingerprint had no
-- production caller and why Gate 3's "all three categories" could not be
-- satisfied by a live installation.
--
-- ── WHAT EVERY QUERY HERE MUST RETURN, AND WHY ───────────────────────────
-- Each returns the subject's identity, the value that crossed the
-- boundary, and — critically — the RE-ARM TOKEN: the field whose change
-- means "this is a new occurrence, alert again". ThresholdFingerprint
-- calls it `bucket`, and it is what stops a threshold alert firing once
-- per evaluation pass forever. For fuel it is the expiry timestamp (a
-- refuel moves it); for an inactive member it is their last logoff (a
-- logon moves it); for a contract it is its expiry (a new contract has a
-- new one). None of them is the time of the EVALUATION, which would defeat
-- deduplication entirely — see internal/alerting/dedupe.go.
--
-- ── "NO DATA" IS NEVER "ZERO" ────────────────────────────────────────────
-- Each query excludes subjects it has no reading for rather than treating
-- a missing value as a boundary crossing. A structure with a NULL
-- fuel_expires has not been observed to be low on fuel; a starbase whose
-- detail fan-out has not run yet has an EMPTY fuels array, not an empty
-- fuel bay. SRS §6's empty-versus-unavailable rule is not only about API
-- responses.

-- name: ListStructuresLowOnFuel :many
-- corporation.structure.fuel_low. §4.4 names
-- /corporations/{corporation_id}/structures as the source route, and
-- app.corporation_structure.fuel_expires is what that route delivers.
--
-- Structures whose fuel has ALREADY run out are included, deliberately: a
-- structure that went dark is the most severe reading this threshold has,
-- and excluding it would mean the alert stops exactly when it matters
-- most. It does not re-alert, because fuel_expires — the re-arm token —
-- does not move again until somebody refuels.
--
-- NULL fuel_expires is excluded: a structure the corporation cannot see
-- fuel for (no Station_Manager role, or a structure type that reports
-- none) is not a structure with no fuel.
SELECT s.corporation_id,
       s.structure_id,
       s.type_id,
       s.system_id,
       s.state,
       s.fuel_expires
  FROM app.corporation_structure s
 WHERE s.fuel_expires IS NOT NULL
   AND s.fuel_expires <= now() + sqlc.arg(within)::interval
 ORDER BY s.fuel_expires, s.corporation_id, s.structure_id;

-- name: ListStarbasesLowOnFuel :many
-- corporation.starbase.fuel_low. §4.4 names the starbase DETAIL route,
-- and Phase 8.1 wired the fan-out for exactly this: app.starbase_detail
-- .fuels is `[{"type_id": N, "quantity": M}]` and is the only place the
-- fuel bay's contents land.
--
-- A starbase burns fuel blocks at a rate that depends on its tower size,
-- which lives in the SDE and is not read here, so this threshold is over
-- the QUANTITY REMAINING rather than over an expiry timestamp. That
-- difference is why the caller buckets the quantity to build a re-arm
-- token instead of using it directly — see internal/alerting/threshold.go.
--
-- jsonb_array_length > 0 is the empty-versus-unavailable guard and it is
-- load-bearing: app.starbase_detail.fuels DEFAULTS to '[]', so every
-- starbase whose detail fan-out has not run yet would otherwise sum to
-- zero and be reported as a tower about to go dark. "We have not fetched
-- the fuel bay" and "the fuel bay is empty" must not be the same reading.
SELECT d.corporation_id,
       d.starbase_id,
       d.system_id,
       d.state,
       q.fuel_quantity
  FROM app.starbase_detail d
  CROSS JOIN LATERAL (
        SELECT coalesce(sum((f->>'quantity')::bigint), 0)::bigint AS fuel_quantity
          FROM jsonb_array_elements(d.fuels) f
       ) q
 WHERE jsonb_array_length(d.fuels) > 0
   AND q.fuel_quantity <= sqlc.arg(below)::bigint
 ORDER BY q.fuel_quantity, d.corporation_id, d.starbase_id;

-- name: ListInactiveCorporationMembers :many
-- corporation.member.inactive — HANGAR's equivalent of the upstream's
-- observer-computed `inactive_member`, over
-- /corporations/{corporation_id}/membertracking.
--
-- logoff_date is both the boundary and the re-arm token, which is the
-- neatest case of the four: the member logs in, ESI reports a new
-- logoff_date when they leave, and the next crossing is a genuinely new
-- occurrence with a genuinely new fingerprint.
--
-- NULL logoff_date is excluded. It means membertracking has never seen
-- this character log off — a brand-new member, or one whose session
-- history predates the corporation's tracking — and reading it as
-- "inactive since the beginning of time" would put every new recruit on
-- the inactive list.
SELECT m.corporation_id,
       m.character_id,
       m.logon_date,
       m.logoff_date
  FROM app.corporation_member_tracking m
 WHERE m.logoff_date IS NOT NULL
   AND m.logoff_date < now() - sqlc.arg(inactive_for)::interval
 ORDER BY m.logoff_date, m.corporation_id, m.character_id;

-- name: ListExpiringCorporationContracts :many
-- corporation.contract.expiring — §4.4's own worked example of a
-- threshold alert ("expiring contracts"), over
-- /corporations/{corporation_id}/contracts.
--
-- Unlike the fuel thresholds this one deliberately does NOT include
-- subjects that have already crossed: an expired contract is not "about to
-- expire", it is finished, and an alert about it is advice nobody can act
-- on. The alert exists to give an operator time to extend or fulfil.
--
-- status is filtered to 'outstanding' because a contract that has been
-- accepted, completed, cancelled or rejected has an expiry date that is no
-- longer meaningful. It is an OPEN VOCABULARY value compared verbatim
-- (Principle 14) — HANGAR does not enumerate CCP's status values, it just
-- knows which one means "still waiting".
SELECT c.owner_id       AS corporation_id,
       c.contract_id,
       c.type,
       c.title,
       c.status,
       c.date_expired
  FROM app.contract c
 WHERE c.owner_kind = 'corporation'
   AND c.status = 'outstanding'
   AND c.date_expired > now()
   AND c.date_expired <= now() + sqlc.arg(within)::interval
 ORDER BY c.date_expired, c.owner_id, c.contract_id;

-- name: CountEnabledSubscriptionsForRoutePaths :many
-- PHASE 20.4. §4.4 makes "a threshold alert whose source route is not in
-- the SYNC SET" a build-time error, and internal/alerting/catalogue's
-- ValidateThresholds enforces exactly that at build time. This is its
-- RUNTIME counterpart, and it catches a different failure the build cannot
-- see: the route is in the sync set, in the catalogue, not blocked by the
-- pin — and this particular installation has no ENABLED subscription to
-- it, because no character has granted the scope it needs.
--
-- The threshold then generates zero alerts on an installation where
-- nothing is wrong with the threshold, which is indistinguishable from
-- "everything is fine" and is the exact confusion §4.4 legislates against
-- one sentence earlier. The evaluator reports it once, loudly, at startup.
--
-- Returns one row per requested path, INCLUDING paths with a count of
-- zero: the caller needs to know which ones are missing, and a query that
-- silently omitted them would answer the wrong question.
SELECT p.upstream_path::text AS upstream_path,
       coalesce(count(s.subscription_id) FILTER (WHERE s.enabled), 0)::bigint AS enabled_subscriptions,
       coalesce(bool_or(r.route_id IS NOT NULL), false)::boolean AS route_catalogued
  FROM unnest(sqlc.arg(upstream_paths)::text[]) AS p(upstream_path)
  LEFT JOIN app.esi_route r
         ON r.upstream_path = p.upstream_path
        AND NOT r.blocked_by_pin
        AND r.retired_at IS NULL
  LEFT JOIN app.sync_subscription s ON s.route_id = r.route_id
 GROUP BY p.upstream_path
 ORDER BY p.upstream_path;
