// Query/mutation hooks for every Phase 18 admin surface. One file for the
// whole admin section (rather than one per feature folder) because these
// screens share a cache namespace — acknowledging a scope invalidates the
// unknown-scope board, advancing the pin invalidates the blocked board AND
// sync health AND the pin history — and colocating the keys is what keeps
// those invalidations honest.
//
// Every path below is a literal from web/src/api/schema.d.ts, so a route
// renamed on the Go side is a TypeScript error here rather than a 404 at
// runtime (Principle 10's whole point).
import { useMutation, useQueryClient, useQuery } from "@tanstack/react-query";

import { apiClient, unwrap } from "@/api/client";
import { collectionQueryOptions, itemQueryOptions } from "@/api/queries/collection";

// ---- read paths (used directly by AutoCollectionTab) ----

export const syncRoutesPath = "/api/v1/admin/sync/routes" as const;
export const syncSubscriptionsPath = "/api/v1/admin/sync/subscriptions" as const;
export const blockedRoutesPath = "/api/v1/admin/esi/catalogue/blocked" as const;
export const pinHistoryPath = "/api/v1/admin/esi/catalogue/pin/history" as const;
export const rateLimitsPath = "/api/v1/admin/esi/ratelimits" as const;
export const replicasPath = "/api/v1/admin/esi/replicas" as const;
export const unknownScopesPath = "/api/v1/admin/scopes/unknown" as const;
export const platformsPath = "/api/v1/admin/platforms" as const;
export const provisioningAuditPath = "/api/v1/admin/provisioning/audit" as const;
export const deadLetterPath = "/api/v1/admin/alerts/dead-letter" as const;
export const unknownTypesPath = "/api/v1/admin/alerts/unknown-types" as const;
export const securityLogPath = "/api/v1/admin/security-log" as const;
export const usersPath = "/api/v1/admin/users" as const;

// ---- shared query options ----

export const syncHealthQueryOptions = () =>
  itemQueryOptions("/api/v1/admin/sync/health", {}, ["admin", "sync", "health"]);

export const errorLimitQueryOptions = () =>
  itemQueryOptions("/api/v1/admin/esi/errorlimit", {}, ["admin", "esi", "errorlimit"]);

export const platformsQueryOptions = () =>
  collectionQueryOptions(platformsPath, {}, ["admin", "platforms"]);

export function usePlatformGroups(platformId: string) {
  return useQuery(
    collectionQueryOptions(
      "/api/v1/admin/platforms/{id}/groups",
      { params: { path: { id: platformId } } },
      ["admin", "platforms", platformId, "groups"],
    ),
  );
}

export function usePlatformRules(platformId: string) {
  return useQuery(
    collectionQueryOptions(
      "/api/v1/admin/platforms/{id}/rules",
      { params: { path: { id: platformId } } },
      ["admin", "platforms", platformId, "rules"],
    ),
  );
}

export function useExposureBoard(platformId: string) {
  return useQuery(
    collectionQueryOptions(
      "/api/v1/admin/provisioning/exposures",
      { params: { query: { platform_id: platformId } } },
      ["admin", "provisioning", "exposures", platformId],
    ),
  );
}

// ---- pin advance ----

/** One route whose blocked-by-pin state changes across the candidate date. */
export interface RouteChange {
  operation_id: string;
  method: string;
  upstream_path: string;
  compatibility_date: string;
}

/** Both directions — see internal/esi/catalogue/diff.go. */
export interface RouteDiff {
  old_pin: string;
  new_pin: string;
  newly_unblocked: RouteChange[];
  newly_blocked: RouteChange[];
  unchanged: number;
}

export interface PinPreview {
  current_pin: string;
  candidate_pin: string;
  d_max: string;
  d_max_source: string;
  within_bounds: boolean;
  diff: RouteDiff;
}

/**
 * POST /api/v1/admin/esi/catalogue/pin/preview — non-mutating. The pin
 * cannot move until the administrator has seen this and confirmed it
 * (Principle 12), which is why it is a separate endpoint rather than a
 * flag on the advance.
 */
export function usePinPreview() {
  return useMutation({
    mutationFn: async (newPin: string): Promise<PinPreview> => {
      const result = await apiClient.POST(
        "/api/v1/admin/esi/catalogue/pin/preview",
        { body: { new_pin: newPin } },
      );
      return unwrap(result).data as unknown as PinPreview;
    },
  });
}

/**
 * POST /api/v1/admin/esi/catalogue/pin — the mutation. The server refuses
 * a candidate newer than D_max with a 422 regardless of what the client
 * allowed, so the disabled button in PinAdvance is a courtesy, not the
 * check (see catalogue.AdvancePin).
 */
export function useAdvancePin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (newPin: string) => {
      const result = await apiClient.POST("/api/v1/admin/esi/catalogue/pin", {
        body: { new_pin: newPin },
      });
      return unwrap(result);
    },
    onSuccess: () => {
      // The pin decides which routes are blocked and which are schedulable,
      // so it invalidates the blocked board, the catalogue, sync health and
      // the history that just gained a row.
      void queryClient.invalidateQueries({ queryKey: ["admin", "esi"] });
      void queryClient.invalidateQueries({ queryKey: ["admin", "sync"] });
    },
  });
}

// ---- acknowledge actions (the two unknown boards) ----

export function useAcknowledgeScope() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (scope: string) => {
      const result = await apiClient.POST(
        "/api/v1/admin/scopes/unknown/acknowledge",
        { body: { scope } },
      );
      return unwrap(result);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "scopes"] });
    },
  });
}

export function useAcknowledgeNotificationType() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (type: string) => {
      const result = await apiClient.POST(
        "/api/v1/admin/alerts/unknown-types/{type}/acknowledge",
        { params: { path: { type } } },
      );
      return unwrap(result);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "alerts"] });
    },
  });
}

export function useRequeueDeadLetter() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const result = await apiClient.POST(
        "/api/v1/admin/alerts/dead-letter/{id}/requeue",
        { params: { path: { id } } },
      );
      return unwrap(result);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "alerts"] });
    },
  });
}

// ---- entitlement rule editor ----

/**
 * `effect` is a closed set on the Go side too
 * (internal/provisioning/entitlement's EffectGrant/EffectDeny) and the
 * generated schema types it as a union, so it is a union here rather than
 * a bare string — an editor that can produce a third value is a 422 the
 * type system should have caught.
 */
export type RuleEffect = "grant" | "deny";

export const RULE_EFFECTS: readonly RuleEffect[] = ["grant", "deny"];

export function asRuleEffect(value: string): RuleEffect {
  return value === "deny" ? "deny" : "grant";
}

export interface RuleDraft {
  source_kind: string;
  source_ref: string;
  group_id: string;
  effect: RuleEffect;
}

export interface RulesPreview {
  diffs: { user_id: string; gained: string[] | null; lost: string[] | null }[];
  /**
   * Opaque. PUT .../rules requires it back verbatim; the server recomputes
   * it over the rules actually submitted and refuses a mismatch, so a rule
   * set that was edited after previewing cannot be saved (see
   * RuleSetPreviewToken in internal/api/v1/admin_provisioning.go). Never
   * construct, parse or reuse one across rule sets.
   */
  preview_token: string;
}

export function usePreviewRules(platformId: string) {
  return useMutation({
    mutationFn: async (rules: RuleDraft[]): Promise<RulesPreview> => {
      const result = await apiClient.POST(
        "/api/v1/admin/platforms/{id}/rules/preview",
        { params: { path: { id: platformId } }, body: { rules } },
      );
      return unwrap(result) as unknown as RulesPreview;
    },
  });
}

export function useSaveRules(platformId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: { rules: RuleDraft[]; previewToken: string }) => {
      const result = await apiClient.PUT("/api/v1/admin/platforms/{id}/rules", {
        params: { path: { id: platformId } },
        body: { rules: input.rules, preview_token: input.previewToken },
      });
      return unwrap(result);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "platforms", platformId],
      });
    },
  });
}

export function useLockdown(platformId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: { lockedDown: boolean; reason: string }) => {
      const result = await apiClient.POST(
        "/api/v1/admin/platforms/{id}/lockdown",
        {
          params: { path: { id: platformId } },
          body: { locked_down: input.lockedDown, reason: input.reason },
        },
      );
      return unwrap(result);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "platforms"] });
    },
  });
}
