// Phase 17 exit criterion `TestContractDetailRendersItemsAndBids`:
// including the empty-items courier case — "render 'no items' rather than
// a loading state that never resolves."
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/i18n";
import { CharacterContracts } from "./CharacterContracts";

const CHARACTER_ID = 555;

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false } },
  });
}

const emptySync = { last_modified_at: null, next_due_at: null, stale: false };

describe("CharacterContracts drawer", () => {
  it("renders 'no items' (not a stuck loading state) for a courier contract with zero items, and renders bids that do exist", async () => {
    const queryClient = makeClient();
    const contractId = 42;

    queryClient.setQueryData(["characters", CHARACTER_ID, "contracts"], {
      rows: [
        {
          contract_id: contractId,
          type: "courier",
          status: "outstanding",
          price: "0",
          reward: "1000000.00",
          collateral: "0",
          date_expired: null,
        },
      ],
      sync: emptySync,
    });
    // Courier contracts legitimately have zero items — the backend
    // resolves this as an ordinary empty array, not an error or a hang.
    queryClient.setQueryData(
      ["characters", CHARACTER_ID, "contracts", contractId, "items"],
      { rows: [], sync: emptySync },
    );
    queryClient.setQueryData(
      ["characters", CHARACTER_ID, "contracts", contractId, "bids"],
      {
        rows: [{ bid_id: 1, bidder_id: 987, amount: "500000.00" }],
        sync: emptySync,
      },
    );

    render(
      <QueryClientProvider client={queryClient}>
        <CharacterContracts characterId={CHARACTER_ID} />
      </QueryClientProvider>,
    );

    const row = await screen.findByText("courier");
    row.click();

    expect(
      await screen.findByText("This contract has no items."),
    ).not.toBeNull();
    expect(await screen.findByText("987")).not.toBeNull();
  });
});
