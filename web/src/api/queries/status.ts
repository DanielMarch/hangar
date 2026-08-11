// /api/v1/meta/esi-status and /api/v1/meta/server-status are DISTINCT and
// both feed the dashboard (Phase 16 prompt) — esi-status is ESI's own
// service health (drives internal/esi gateway decisions); server-status is
// Tranquility's players/VIP/version. Never conflate them into one query.
//
// server-status renders `_sync.blocked_by_pin` (via the shared Sync
// envelope) until its first successful sync — `data` is `null` in that
// case, and callers MUST render "not yet synced", never "0 players online"
// (that would be a false reading, not an empty result).
import { queryOptions, useSuspenseQuery } from "@tanstack/react-query";

import { apiClient, unwrap } from "@/api/client";
import type { components } from "@/api/schema.d.ts";

export type StatusRow = Record<string, unknown> | null;
export type Sync = components["schemas"]["Sync"];

export const esiStatusQueryOptions = queryOptions({
  queryKey: ["meta", "esi-status"],
  queryFn: async () => {
    const result = await apiClient.GET("/api/v1/meta/esi-status");
    const body = unwrap(result);
    return { data: body.data as StatusRow, sync: body._sync };
  },
  refetchInterval: 60_000,
});

export const serverStatusQueryOptions = queryOptions({
  queryKey: ["meta", "server-status"],
  queryFn: async () => {
    const result = await apiClient.GET("/api/v1/meta/server-status");
    const body = unwrap(result);
    return { data: body.data as StatusRow, sync: body._sync };
  },
  refetchInterval: 60_000,
});

export function useEsiStatus() {
  return useSuspenseQuery(esiStatusQueryOptions);
}

export function useServerStatus() {
  return useSuspenseQuery(serverStatusQueryOptions);
}
