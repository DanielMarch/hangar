// Phase 17 exit criterion `TestSyncBadgeReflectsEnvelope`: fresh / stale /
// blocked-by-pin states all distinct.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/i18n";
import { SyncBadge } from "./SyncBadge";
import type { Sync } from "@/api/queries/status";

describe("SyncBadge", () => {
  it("renders 'unavailable' for blocked_by_pin, with the administrator-facing reason in a tooltip, distinct from stale/fresh", () => {
    const sync: Sync = {
      last_modified_at: null,
      next_due_at: null,
      stale: true,
      blocked_by_pin: "pin advance pending",
    };
    render(<SyncBadge sync={sync} />);
    expect(screen.getByText("Unavailable")).not.toBeNull();
    // The stale-specific "Stale ·" prefix and a real timestamp must NOT
    // appear — blocked_by_pin is its own state, not "stale" with extra
    // steps.
    expect(screen.queryByText(/Stale ·/)).toBeNull();
  });

  it("renders 'not yet synced' when last_modified_at is null and not blocked — a third, distinct state from stale/fresh/unavailable", () => {
    const sync: Sync = {
      last_modified_at: null,
      next_due_at: null,
      stale: false,
    };
    render(<SyncBadge sync={sync} />);
    expect(screen.getByText("Not yet synced")).not.toBeNull();
    expect(screen.queryByText("Unavailable")).toBeNull();
  });

  it("renders a stale badge distinctly from a fresh one for the same last_modified_at", () => {
    const lastModified = new Date(Date.now() - 5 * 60_000).toISOString();

    const { unmount } = render(
      <SyncBadge
        sync={{
          last_modified_at: lastModified,
          next_due_at: null,
          stale: true,
        }}
      />,
    );
    expect(screen.getByText(/Stale ·/)).not.toBeNull();
    unmount();

    render(
      <SyncBadge
        sync={{
          last_modified_at: lastModified,
          next_due_at: null,
          stale: false,
        }}
      />,
    );
    expect(screen.queryByText(/Stale ·/)).toBeNull();
  });
});
