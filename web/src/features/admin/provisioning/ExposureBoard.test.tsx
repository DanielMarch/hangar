// Phase 18 exit criterion `TestExposureBoardShowsExactAges` — the
// rendering half. age.test.ts covers the arithmetic.
//
// What this asserts, and why each matters:
//
//   * the age is derived from `event_at` and from nothing else. A row with
//     a plausible-but-wrong alternative timestamp on it (last_reconciled_at,
//     which is what "job start" would look like on a real row) must not
//     produce an age from that column.
//   * the age is EXACT — "1h 04m 09s", not "about an hour".
//   * a row past the 60s revocation SLO is visibly called out rather than
//     left as one row among many.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";
import { ExposureBoard } from "./ExposureBoard";

const PLATFORM = "3f7c1c9a-0000-4000-8000-000000000001";
const emptySync = { last_modified_at: null, next_due_at: null, stale: false };
const NOW = "2026-08-11T12:00:00Z";

function renderBoard(rows: Record<string, unknown>[]) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false } },
  });
  queryClient.setQueryData(
    ["admin", "provisioning", "exposures", PLATFORM],
    { rows, sync: emptySync },
  );
  return render(
    <QueryClientProvider client={queryClient}>
      <ExposureBoard platformId={PLATFORM} />
    </QueryClientProvider>,
  );
}

describe("ExposureBoard ages", () => {
  beforeEach(() => {
    // `toFake: ["Date"]` and nothing else. Freezing setTimeout/setInterval
    // too would deadlock Testing Library's own waitFor polling (it detects
    // Jest's fake timers, not Vitest's, so it never advances them), and
    // the board's 1s refresh tick is not what these assertions are about —
    // a frozen Date.now() is.
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(NOW));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("computes the age from event_at, not from any other timestamp on the row", async () => {
    renderBoard([
      {
        audit_id: "a1",
        exposure_kind: "pending",
        user_id: "u-1",
        action: "revoke",
        reason: "token invalidated",
        event_at: "2026-08-11T10:55:51Z", // 1h 04m 09s ago
        // A decoy: this is what "measured from job start" would look like,
        // and it must not be what the board reports.
        last_reconciled_at: "2026-08-11T11:59:55Z", // 5s ago
      },
    ]);

    expect(await screen.findByText("1h 04m 09s")).not.toBeNull();
    expect(screen.queryByText("5s")).toBeNull();
  });

  it("flags a pending revocation past the 60s SLO", async () => {
    renderBoard([
      {
        audit_id: "a1",
        exposure_kind: "pending",
        user_id: "u-late",
        event_at: "2026-08-11T11:58:00Z", // 2m 00s — past the SLO
      },
      {
        audit_id: "a2",
        exposure_kind: "pending",
        user_id: "u-ok",
        event_at: "2026-08-11T11:59:30Z", // 30s — within the SLO
      },
    ]);

    const breaches = await screen.findAllByTestId("exposure-age-breach");
    expect(breaches).toHaveLength(1);
    expect(breaches[0].textContent).toBe("2m 00s");

    const fine = screen.getAllByTestId("exposure-age");
    expect(fine).toHaveLength(1);
    expect(fine[0].textContent).toBe("30s");
  });

  it("shows a mismatched row (which has no event_at) as having no age, rather than inventing one", async () => {
    renderBoard([
      {
        user_id: "u-2",
        exposure_kind: "mismatched",
        desired_groups: ["a"],
        actual_groups: [],
        last_reconciled_at: "2026-08-11T11:00:00Z",
      },
    ]);

    expect(await screen.findByText("u-2")).not.toBeNull();
    expect(screen.queryByTestId("exposure-age")).toBeNull();
    expect(screen.queryByTestId("exposure-age-breach")).toBeNull();
  });
});
