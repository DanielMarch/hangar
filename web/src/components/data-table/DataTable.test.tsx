// Phase 17 exit criterion `TestVirtualizedWalletScrolls100kAt60fps`.
//
// jsdom has no real compositor/rAF budget, so a literal "60fps" can't be
// measured here — that would need a browser (Playwright, already a
// devDependency, has no e2e suite wired up yet). What IS verifiable, and
// is the actual mechanism the 60fps target depends on, is virtualization
// itself: given 100k rows, DataTable must mount only a small, bounded
// number of row DOM nodes (@tanstack/react-virtual's windowed rendering),
// not one element per row — that's what keeps any single frame's paint
// work constant regardless of row count. This test asserts that bound and
// that mounting the table happens well within a single frame's budget's
// worth of headroom for the setup work involved.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/i18n";
import { DataTable } from "./DataTable";
import { numberColumn, textColumn } from "./columns";

// web/src/test-setup.ts stubs clientHeight/offsetHeight globally so
// @tanstack/react-virtual windows rows the way it would in a real browser
// rather than (correctly, for jsdom's real 0px layout) rendering none.
const ROW_COUNT = 100_000;
const columns = [numberColumn("id", "id"), textColumn("label", "label")];

function makeRows(n: number) {
  return Array.from({ length: n }, (_, i) => ({ id: i, label: `row-${i}` }));
}

describe("DataTable virtualization at 100k rows", () => {
  it("mounts only a small, bounded window of row elements, not 100k", () => {
    const data = makeRows(ROW_COUNT);
    const start = performance.now();
    render(<DataTable columns={columns} data={data} />);
    const elapsed = performance.now() - start;

    const renderedRows = screen.getAllByRole("row");
    // Overscan (12) either side of whatever fits jsdom's zero-height
    // viewport is a small double-digit number — nowhere near 100k. This is
    // the property that makes 60fps achievable at all: constant per-frame
    // work independent of total row count.
    expect(renderedRows.length).toBeLessThan(200);
    expect(renderedRows.length).toBeGreaterThan(0);

    // Generous headroom, not a real frame-budget assertion (see file
    // banner) — catches an accidental "render every row" regression, which
    // would blow this past several seconds, not just one frame.
    expect(elapsed).toBeLessThan(2000);
  });

  it("keeps the scroll container's DOM node count independent of row count (10 vs 100k rows)", () => {
    const { container: small } = render(
      <DataTable columns={columns} data={makeRows(10)} />,
    );
    const smallRowCount = small.querySelectorAll('[role="row"]').length;

    const { container: large } = render(
      <DataTable columns={columns} data={makeRows(ROW_COUNT)} />,
    );
    const largeRowCount = large.querySelectorAll('[role="row"]').length;

    // Both render a small window; the 100k-row table doesn't render
    // meaningfully more DOM than the 10-row one.
    expect(largeRowCount - smallRowCount).toBeLessThan(50);
  });
});
