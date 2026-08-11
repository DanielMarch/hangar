// Phase 17 exit criterion `TestAssetTreeDepth5Under2s`: "one request,
// bounded depth." internal/store/asset.go's AssetTree returns the whole
// subtree (up to depth 10, well past the 5 the roadmap targets) as ONE
// flat array of { asset, depth, path } nodes — this test seeds the
// TanStack Query cache with exactly that shape at depth 0..4, spies on
// `fetch` to prove no additional request fires per level, and asserts
// every depth renders within the 2s budget.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
  type MockInstance,
} from "vitest";

import "@/i18n";
import { CharacterAssets } from "./CharacterAssets";

const CHARACTER_ID = 12345;
const ROOT_LOCATION_ID = 60003760;

function fiveLevelTree() {
  // path/depth exactly as internal/store/asset.go's BuildAssetTree emits —
  // each node's own item_id, nested one level deeper than its parent.
  return Array.from({ length: 5 }, (_, depth) => ({
    asset: {
      item_id: 1000 + depth,
      type_id: 34,
      quantity: 1,
      name: `Depth-${depth}`,
    },
    depth,
    path: Array.from({ length: depth + 1 }, (_, i) => 1000 + i),
  }));
}

describe("CharacterAssets tree", () => {
  let fetchSpy: MockInstance<typeof globalThis.fetch>;

  beforeEach(() => {
    fetchSpy = vi.spyOn(globalThis, "fetch");
  });
  afterEach(() => {
    fetchSpy.mockRestore();
  });

  it("renders a 5-level tree from one pre-fetched response, no extra requests, under 2s", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity, retry: false } },
    });

    // Seed exactly what CharacterAssets' two useCollection calls would
    // have received from the network — the point of this test is that,
    // GIVEN one response per query, no per-depth-level fetching happens.
    queryClient.setQueryData(["characters", CHARACTER_ID, "assets"], {
      rows: [{ item_id: 1, location_id: ROOT_LOCATION_ID }],
      sync: { last_modified_at: null, next_due_at: null, stale: false },
    });
    queryClient.setQueryData(
      ["characters", CHARACTER_ID, "assets", "tree", ROOT_LOCATION_ID],
      {
        rows: fiveLevelTree(),
        sync: { last_modified_at: null, next_due_at: null, stale: false },
      },
    );

    const start = performance.now();
    render(
      <QueryClientProvider client={queryClient}>
        <CharacterAssets characterId={CHARACTER_ID} />
      </QueryClientProvider>,
    );

    for (let depth = 0; depth < 5; depth++) {
      expect(await screen.findByText(`Depth-${depth}`)).not.toBeNull();
    }
    const elapsed = performance.now() - start;

    expect(elapsed).toBeLessThan(2000);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
