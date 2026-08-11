// Phase 18 exit criteria, browser half:
//   TestPinAdvanceShowsRouteDiffBeforeChange
//   TestPinAdvanceRefusesDateNewerThanDMax
//
// This is the half jsdom verifies weakly. A component test can assert
// `button.disabled === true`; only a real browser can tell you that a real
// click on that button produces no request, that the gate survives the
// re-render an edit causes, and that the pin in the database is still
// where it was.
//
// Every assertion below about "the pin did not move" is made against the
// API's own view of the pin, not against the DOM.
import { expect, test } from "./fixtures";
import { BEYOND_D_MAX, SEEDED_D_MAX, SEEDED_PIN } from "./global-setup";

async function currentPin(request: {
  get: (url: string) => Promise<{ json: () => Promise<unknown> }>;
}): Promise<string> {
  const res = await request.get("/api/v1/admin/sync/health");
  const body = (await res.json()) as { data: { compatibility_pin: string } };
  return body.data.compatibility_pin;
}

test.describe("pin advance", () => {
  test("shows the full route diff and requires confirmation before the pin moves", async ({
    page,
    request,
  }) => {
    // The suite is serial and this spec is the only one that moves the
    // pin, but read it rather than assume it: a re-run against an already
    // advanced database should fail loudly here, not silently pass later.
    expect(await currentPin(request)).toBe(SEEDED_PIN);

    await page.goto("/admin/esi");
    await expect(page.getByTestId("pin-advance")).toBeVisible();

    // No advance control exists before a preview.
    await expect(page.getByTestId("pin-advance-button")).toHaveCount(0);

    const advanceCalls: string[] = [];
    page.on("request", (r) => {
      if (r.method() === "POST" && r.url().endsWith("/api/v1/admin/esi/catalogue/pin")) {
        advanceCalls.push(r.url());
      }
    });

    await page.getByTestId("pin-candidate").fill(SEEDED_D_MAX);
    await page.getByTestId("pin-preview-button").click();

    // The diff is on screen, in BOTH directions, before anything moves.
    const diff = page.getByTestId("pin-diff");
    await expect(diff).toBeVisible();
    await expect(page.getByTestId("pin-diff-unblocked")).toContainText("/e2e_get_mid/");
    await expect(page.getByTestId("pin-diff-unblocked")).toContainText("/e2e_get_new/");
    await expect(page.getByTestId("pin-diff-blocked")).toBeVisible();

    // Previewing is non-mutating — the API still reports the old pin.
    expect(await currentPin(request)).toBe(SEEDED_PIN);

    // The advance is genuinely unclickable, not merely styled as such.
    const advance = page.getByTestId("pin-advance-button");
    await expect(advance).toBeDisabled();
    await advance.click({ force: true, trial: false }).catch(() => {
      // A forced click on a disabled control may throw; either way the
      // assertion that matters is that no request was made.
    });
    expect(advanceCalls).toHaveLength(0);
    expect(await currentPin(request)).toBe(SEEDED_PIN);

    // Editing the date after previewing revokes the preview entirely —
    // the case the gate exists for (preview a quiet week, advance across a
    // noisy one).
    await page.getByTestId("pin-candidate").fill("2026-08-10");
    await expect(page.getByTestId("pin-preview-stale")).toBeVisible();
    await expect(page.getByTestId("pin-advance-button")).toHaveCount(0);

    // Preview again, confirm, and only then advance.
    await page.getByTestId("pin-candidate").fill(SEEDED_D_MAX);
    await page.getByTestId("pin-preview-button").click();
    await expect(page.getByTestId("pin-diff")).toBeVisible();
    await page.getByTestId("pin-confirm").check();
    await expect(page.getByTestId("pin-advance-button")).toBeEnabled();
    await page.getByTestId("pin-advance-button").click();

    await expect(page.getByTestId("pin-advanced")).toBeVisible();
    expect(advanceCalls).toHaveLength(1);
    expect(await currentPin(request)).toBe(SEEDED_D_MAX);

    // The advance recorded a real diff, not the `{}` placeholder every
    // pre-Phase-18 row carries (SRS defect B13).
    const history = await request.get("/api/v1/admin/esi/catalogue/pin/history");
    const body = (await history.json()) as {
      data: { new_pin: string; route_diff: Record<string, unknown> }[];
    };
    const latest = body.data[0];
    expect(latest.new_pin).toBe(SEEDED_D_MAX);
    // Nested JSON on the wire, not a hex string (SRS defect B12) — if the
    // converter regressed this would be a string and the next line throws.
    expect(Array.isArray(latest.route_diff.newly_unblocked)).toBe(true);
    expect(latest.route_diff.newly_unblocked).toHaveLength(2);
  });

  test("refuses a date newer than D_max, client- and server-side", async ({
    page,
    request,
  }) => {
    const before = await currentPin(request);

    await page.goto("/admin/esi");
    await page.getByTestId("pin-candidate").fill(BEYOND_D_MAX);
    await page.getByTestId("pin-preview-button").click();

    // The preview still answers — an out-of-range candidate is previewed,
    // not refused, so the operator learns what the ceiling actually is.
    await expect(page.getByTestId("pin-out-of-range")).toContainText(SEEDED_D_MAX);
    await expect(page.getByTestId("pin-confirm")).toBeDisabled();
    await expect(page.getByTestId("pin-advance-button")).toBeDisabled();

    // The server half. A UI-only bound check is bypassed by any direct API
    // call, so this is the assertion that makes the criterion mean
    // something: the same request the disabled button would have sent,
    // sent directly, is refused with a 422.
    const direct = await request.post("/api/v1/admin/esi/catalogue/pin", {
      data: { new_pin: BEYOND_D_MAX },
    });
    expect(direct.status()).toBe(422);
    expect(await direct.text()).toContain("D_max");

    expect(await currentPin(request)).toBe(before);
  });
});
