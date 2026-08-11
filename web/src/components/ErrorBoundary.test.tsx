// Phase 17 exit criterion `TestErrorBoundaryIsolatesModule`: a failing
// wallet panel does not take down the character route.
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import "@/i18n";
import { ErrorBoundary } from "./ErrorBoundary";

function ThrowingModule(): React.ReactNode {
  throw new Error("wallet panel exploded");
}

function OtherModule() {
  return <div>Other module rendered fine</div>;
}

describe("ErrorBoundary", () => {
  it("isolates a failing sibling module — the rest of the route keeps rendering", () => {
    // React logs the caught error to console.error; expected noise for
    // this test, not a real failure signal.
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    render(
      <div>
        <ErrorBoundary>
          <ThrowingModule />
        </ErrorBoundary>
        <ErrorBoundary>
          <OtherModule />
        </ErrorBoundary>
      </div>,
    );

    // The failing module's own boundary shows the local retry fallback...
    expect(screen.getByText("Something went wrong")).not.toBeNull();
    expect(screen.getByText("Retry")).not.toBeNull();
    // ...while the sibling module, in its own boundary, is entirely
    // unaffected — the route as a whole never crashed.
    expect(screen.getByText("Other module rendered fine")).not.toBeNull();

    consoleSpy.mockRestore();
  });
});
