-- Project HANGAR — Phase 12: Discord invalid-request budget.
--
-- 01_ARCHITECTURE.md §9.3 / roadmap: "10,000 responses of 401/403/429 per
-- rolling 10 minutes trips a Cloudflare ban... This budget is
-- installation-wide and shares the Postgres-backed counter mechanism from
-- §5.7." §5.7 (Governor 2, app.esi_error_budget, 00006) is explicitly a
-- FIXED window, not a rolling one — a true rolling window needs the
-- multi-row ledger shape from §5.5/§5.6 (app.esi_ledger_entry), which
-- nothing in §9.3 cites. Taking "shares the mechanism from §5.7" as the
-- operative instruction (it names a concrete, already-proven, testable
-- design; "rolling" is the surrounding prose's looser paraphrase), this
-- table is app.esi_error_budget's shape verbatim, substituting the 10-
-- minute/10,000 numbers for §5.7's 60-second/100. Reported in the Phase 12
-- handoff per the "report spec conflicts rather than silently resolving
-- them" instruction.
--
-- +goose Up

CREATE TABLE app.discord_invalid_budget (
    id            smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    window_start  timestamptz NOT NULL DEFAULT now(),
    invalid_count integer     NOT NULL DEFAULT 0,
    paused        boolean     NOT NULL DEFAULT false,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE app.discord_invalid_budget;
