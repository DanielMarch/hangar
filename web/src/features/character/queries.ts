// Every /api/v1/characters/{id}/... query this feature uses, in one file
// so a route/component never hand-rolls a fetch. All share the
// "characters.view" RBAC permission (internal/api/v1/characters.go's file
// banner) — a 403 on the character-detail query almost always means every
// tab will also 403, but each tab still degrades independently via
// CollectionTable/PermissionGate rather than assuming that.
import { itemQueryOptions } from "@/api/queries/collection";

export const characterQueryOptions = (id: number) =>
  itemQueryOptions("/api/v1/characters/{id}", { params: { path: { id } } }, [
    "characters",
    id,
  ]);

export const characterSkillsPath = "/api/v1/characters/{id}/skills" as const;
export const characterSkillqueuePath =
  "/api/v1/characters/{id}/skillqueue" as const;
export const characterAssetsPath = "/api/v1/characters/{id}/assets" as const;
export const characterAssetTreePath =
  "/api/v1/characters/{id}/assets/tree/{location_id}" as const;
export const characterWalletJournalPath =
  "/api/v1/characters/{id}/wallet/journal" as const;
export const characterWalletTransactionsPath =
  "/api/v1/characters/{id}/wallet/transactions" as const;
export const characterContractsPath =
  "/api/v1/characters/{id}/contracts" as const;
export const characterContractItemsPath =
  "/api/v1/characters/{id}/contracts/{sub_id}/items" as const;
export const characterContractBidsPath =
  "/api/v1/characters/{id}/contracts/{sub_id}/bids" as const;
export const characterMailPath = "/api/v1/characters/{id}/mail" as const;
export const characterMailBodyPath =
  "/api/v1/characters/{id}/mail/{sub_id}" as const;
export const characterPlanetsPath = "/api/v1/characters/{id}/planets" as const;
export const characterPlanetDetailPath =
  "/api/v1/characters/{id}/planets/{sub_id}" as const;
export const characterCalendarPath =
  "/api/v1/characters/{id}/calendar" as const;
export const characterIndustryJobsPath =
  "/api/v1/characters/{id}/industry/jobs" as const;
export const characterKillmailsPath =
  "/api/v1/characters/{id}/killmails" as const;
export const characterFittingsPath =
  "/api/v1/characters/{id}/fittings" as const;
export const characterIntelPath = "/api/v1/characters/{id}/intel" as const;
