// Every /api/v1/corporations/{id}/... query this feature uses. All share
// the "corporations.view" RBAC permission (internal/api/v1/corporations.go's
// file banner) — same one-permission-covers-everything shape as
// characters.go.
import { itemQueryOptions } from "@/api/queries/collection";

export const corporationQueryOptions = (id: number) =>
  itemQueryOptions("/api/v1/corporations/{id}", { params: { path: { id } } }, [
    "corporations",
    id,
  ]);

export const corporationMembersPath =
  "/api/v1/corporations/{id}/members" as const;
export const corporationWalletsPath =
  "/api/v1/corporations/{id}/wallets" as const;
export const corporationWalletJournalPath =
  "/api/v1/corporations/{id}/wallets/{division}/journal" as const;
export const corporationWalletTransactionsPath =
  "/api/v1/corporations/{id}/wallets/{division}/transactions" as const;
export const corporationLedgerBountiesPath =
  "/api/v1/corporations/{id}/ledger/bounties" as const;
export const corporationLedgerMiningPath =
  "/api/v1/corporations/{id}/ledger/mining" as const;
export const corporationLedgerPiPath =
  "/api/v1/corporations/{id}/ledger/pi" as const;
export const corporationStructuresPath =
  "/api/v1/corporations/{id}/structures" as const;
export const corporationSkyhooksPath =
  "/api/v1/corporations/{id}/structures/skyhooks" as const;
export const corporationStarbasesPath =
  "/api/v1/corporations/{id}/starbases" as const;
export const corporationProjectsPath =
  "/api/v1/corporations/{id}/projects" as const;
export const corporationProjectContributorsPath =
  "/api/v1/corporations/{id}/projects/{project_id}/contributors" as const;
export const corporationMiningExtractionsPath =
  "/api/v1/corporations/{id}/mining/extractions" as const;
export const corporationMiningObserversPath =
  "/api/v1/corporations/{id}/mining/observers" as const;
