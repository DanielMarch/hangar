// Phase 18 exit criteria `TestPinAdvanceShowsRouteDiffBeforeChange` and
// `TestPinAdvanceRefusesDateNewerThanDMax` (client half), plus the
// roadmap's "no routes changed is a legitimate answer, not an empty
// state" edge case.
//
// The browser-level version of the confirm flow lives in
// web/e2e/pin-advance.spec.ts — jsdom verifies a disabled attribute
// weakly, and a confirmation gate is exactly the kind of thing that is
// worth driving in a real browser. The server halves of both criteria are
// in internal/esi/catalogue/pin_integration_test.go; neither client
// assertion below is the criterion on its own.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";
import { apiClient } from "@/api/client";
import { PinAdvance } from "./PinAdvance";

const ADVANCE_URL = "/api/v1/admin/esi/catalogue/pin";
const PREVIEW_URL = "/api/v1/admin/esi/catalogue/pin/preview";

function preview(overrides: Record<string, unknown> = {}) {
  return {
    data: {
      current_pin: "2026-08-04",
      candidate_pin: "2026-08-11",
      d_max: "2026-08-11",
      d_max_source: "recorded",
      within_bounds: true,
      diff: {
        old_pin: "2026-08-04",
        new_pin: "2026-08-11",
        newly_unblocked: [
          {
            operation_id: "get_characters_id_titles",
            method: "GET",
            upstream_path: "/characters/{character_id}/titles/",
            compatibility_date: "2026-08-07",
          },
        ],
        newly_blocked: [
          {
            operation_id: "get_corporations_id_shares",
            method: "GET",
            upstream_path: "/corporations/{corporation_id}/shares/",
            compatibility_date: "2026-08-09",
          },
        ],
        unchanged: 410,
      },
      ...overrides,
    },
    _sync: { last_modified_at: null, next_due_at: null, stale: false },
  };
}

let calls: string[] = [];

// Spying on apiClient.POST rather than on globalThis.fetch: openapi-fetch
// captures `globalThis.fetch` at createClient() time (dist/index.mjs:12),
// which happens when @/api/client is imported — so a fetch stubbed after
// that import is never consulted, and the component would just render its
// error branch. What these tests are about is the component's own gate
// logic; the wire contract itself is covered by the Go handler tests and,
// end to end, by web/e2e/pin-advance.spec.ts.
function mockApi(previewBody: unknown) {
  calls = [];
  vi.spyOn(apiClient, "POST").mockImplementation((async (path: string) => {
    calls.push(path);
    return {
      data: path.includes("/preview") ? previewBody : { data: {}, _sync: {} },
      response: new Response(null, { status: 200 }),
    };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- openapi-fetch's POST is an overloaded, path-literal-keyed signature that cannot be satisfied by a single generic mock.
  }) as any);
}

function renderPin() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <PinAdvance currentPin="2026-08-04" />
    </QueryClientProvider>,
  );
}

describe("PinAdvance", () => {
  beforeEach(() => mockApi(preview()));
  afterEach(() => vi.restoreAllMocks());

  it("shows the diff before the pin moves, and cannot advance until it is confirmed", async () => {
    const user = userEvent.setup();
    renderPin();

    // Nothing is advanceable before a preview exists.
    expect(screen.queryByTestId("pin-advance-button")).toBeNull();

    await user.type(screen.getByTestId("pin-candidate"), "2026-08-11");
    await user.click(screen.getByTestId("pin-preview-button"));

    // The diff is on screen — BOTH directions.
    const diff = await screen.findByTestId("pin-diff");
    expect(diff.textContent).toContain("/characters/{character_id}/titles/");
    expect(diff.textContent).toContain("/corporations/{corporation_id}/shares/");
    expect(screen.getByTestId("pin-diff-blocked")).not.toBeNull();
    expect(screen.getByTestId("pin-diff-unblocked")).not.toBeNull();

    // Still nothing has been mutated: only the preview was called.
    expect(calls.every((c) => c.includes(PREVIEW_URL))).toBe(true);

    // The advance is refused until the diff is confirmed.
    const advanceButton = screen.getByTestId("pin-advance-button");
    expect(advanceButton).toHaveProperty("disabled", true);
    await user.click(advanceButton);
    expect(calls.some((c) => c.endsWith(ADVANCE_URL))).toBe(false);

    await user.click(screen.getByTestId("pin-confirm"));
    expect(screen.getByTestId("pin-advance-button")).toHaveProperty(
      "disabled",
      false,
    );
    await user.click(screen.getByTestId("pin-advance-button"));

    await waitFor(() =>
      expect(calls.some((c) => c.endsWith(ADVANCE_URL))).toBe(true),
    );
  });

  it("re-locks the advance when the date is edited after previewing", async () => {
    // Otherwise an operator could preview a quiet week and advance across
    // a noisy one — the case the whole gate exists for.
    const user = userEvent.setup();
    renderPin();

    await user.type(screen.getByTestId("pin-candidate"), "2026-08-11");
    await user.click(screen.getByTestId("pin-preview-button"));
    await screen.findByTestId("pin-diff");
    await user.click(screen.getByTestId("pin-confirm"));
    expect(screen.getByTestId("pin-advance-button")).toHaveProperty(
      "disabled",
      false,
    );

    await user.type(screen.getByTestId("pin-candidate"), "1");

    expect(await screen.findByTestId("pin-preview-stale")).not.toBeNull();
    expect(screen.queryByTestId("pin-advance-button")).toBeNull();
    expect(calls.some((c) => c.endsWith(ADVANCE_URL))).toBe(false);
  });

  it("refuses a candidate newer than D_max", async () => {
    mockApi(
      preview({
        candidate_pin: "2026-12-01",
        d_max: "2026-08-11",
        within_bounds: false,
      }),
    );
    const user = userEvent.setup();
    renderPin();

    await user.type(screen.getByTestId("pin-candidate"), "2026-12-01");
    await user.click(screen.getByTestId("pin-preview-button"));

    const warning = await screen.findByTestId("pin-out-of-range");
    expect(warning.textContent).toContain("2026-08-11");

    // The confirmation itself is unavailable, so the advance is
    // unreachable — and the server refuses it independently anyway.
    expect(screen.getByTestId("pin-confirm")).toHaveProperty("disabled", true);
    expect(screen.getByTestId("pin-advance-button")).toHaveProperty(
      "disabled",
      true,
    );
    expect(calls.some((c) => c.endsWith(ADVANCE_URL))).toBe(false);
  });

  it("states 'no routes change' explicitly rather than rendering a blank panel", async () => {
    // Rendering nothing here invites advancing the pin believing the
    // preview simply failed to load.
    mockApi(
      preview({
        diff: {
          old_pin: "2026-08-04",
          new_pin: "2026-08-06",
          newly_unblocked: [],
          newly_blocked: [],
          unchanged: 412,
        },
      }),
    );
    const user = userEvent.setup();
    renderPin();

    await user.type(screen.getByTestId("pin-candidate"), "2026-08-06");
    await user.click(screen.getByTestId("pin-preview-button"));

    const panel = await screen.findByTestId("pin-diff-no-change");
    expect(panel.textContent).toContain("412");
    // And the advance remains reachable — "nothing changes" is a valid
    // reason to move the pin, not a reason to block it.
    expect(screen.getByTestId("pin-confirm")).toHaveProperty("disabled", false);
  });
});
