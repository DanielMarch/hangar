// Phase 18 exit criterion, browser half:
//   TestRuleEditorRequiresPreviewConfirmation — "saving without preview is
//   impossible"
//
// Both halves of "impossible" are asserted here, because only one of them
// is a UI property:
//
//   * the Save control cannot be reached without previewing AND confirming,
//     and any edit to the rules revokes both;
//   * a request that skips the editor entirely — no preview_token, or a
//     token issued for a different rule set — is refused by the server with
//     a 422. Without that, "impossible" would mean "inconvenient".
import { expect, test } from "./fixtures";
import { GROUP_A_ID, PLATFORM_ID } from "./global-setup";

const RULES_URL = `/api/v1/admin/platforms/${PLATFORM_ID}/rules`;

test.describe("entitlement rule editor", () => {
  test("cannot save without previewing and confirming", async ({ page }) => {
    const saveCalls: string[] = [];
    page.on("request", (r) => {
      if (r.method() === "PUT" && r.url().includes(RULES_URL)) {
        saveCalls.push(r.url());
      }
    });

    await page.goto("/admin/platforms");
    await page.getByText("E2E Discord").first().click();
    await expect(page.getByTestId("rule-editor")).toBeVisible();

    // Add a rule. Nothing has been previewed, so saving is refused.
    await page.getByTestId("rule-add").click();
    await expect(page.getByTestId("rule-row")).toHaveCount(1);
    await expect(page.getByTestId("rule-save-blocked")).toBeVisible();

    const save = page.getByTestId("rule-save-button");
    await expect(save).toBeDisabled();
    await save.click({ force: true }).catch(() => {});
    expect(saveCalls).toHaveLength(0);

    // Preview. The save is still refused — a preview seen is not a preview
    // confirmed, and the confirmation is the part an operator has to read
    // the diff to give.
    await page.getByTestId("rule-preview-button").click();
    await expect(page.getByTestId("rule-preview")).toBeVisible();
    await expect(save).toBeDisabled();
    expect(saveCalls).toHaveLength(0);

    // Confirm — now it is reachable.
    await page.getByTestId("rule-confirm").check();
    await expect(save).toBeEnabled();

    // ...but editing a rule after confirming revokes it again. This is the
    // case that matters: previewing something harmless and saving
    // something else is how an accidental mass revocation happens.
    await page.getByTestId("rule-effect").first().selectOption("deny");
    await expect(page.getByTestId("rule-preview-stale")).toBeVisible();
    await expect(save).toBeDisabled();
    expect(saveCalls).toHaveLength(0);

    // Preview and confirm the edited set, and the save goes through.
    await page.getByTestId("rule-preview-button").click();
    await expect(page.getByTestId("rule-preview")).toBeVisible();
    await page.getByTestId("rule-confirm").check();
    await page.getByTestId("rule-save-button").click();
    await expect(page.getByTestId("rule-saved")).toBeVisible();
    expect(saveCalls).toHaveLength(1);
  });

  test("the server refuses a save that skips the editor", async ({ request }) => {
    const rules = [
      {
        source_kind: "public",
        source_ref: "",
        group_id: GROUP_A_ID,
        effect: "grant",
      },
    ];

    // No token at all.
    const noToken = await request.put(RULES_URL, {
      data: { rules, preview_token: "" },
    });
    expect(noToken.status()).toBe(422);
    expect(await noToken.text()).toContain("preview");

    // A token issued for a DIFFERENT rule set. This is the dangerous case:
    // preview something harmless, submit something else. The server
    // recomputes the digest over the rules it actually received, so the
    // stale token cannot authorise them.
    const previewed = await request.post(`${RULES_URL}/preview`, {
      data: { rules },
    });
    expect(previewed.ok()).toBe(true);
    const token = ((await previewed.json()) as { preview_token: string })
      .preview_token;
    expect(token).toBeTruthy();

    const swapped = await request.put(RULES_URL, {
      data: {
        rules: [{ ...rules[0], effect: "deny" }],
        preview_token: token,
      },
    });
    expect(swapped.status()).toBe(422);

    // The matching token is accepted — the gate is a gate, not a wall.
    const accepted = await request.put(RULES_URL, {
      data: { rules, preview_token: token },
    });
    expect(accepted.ok()).toBe(true);
  });
});
