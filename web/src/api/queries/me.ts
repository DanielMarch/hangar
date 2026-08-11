// Server state only — TanStack Query owns this, never Zustand (SRS §8.3:
// "server data must never be copied into Zustand"). /api/v1/me and
// /api/v1/me/characters need only a resolved session, no RBAC permission
// (SRS §5.2) — every signed-in member can call them.
import { queryOptions, useQuery } from "@tanstack/react-query";

import { apiClient, unwrap } from "@/api/client";

export type MeRow = Record<string, unknown>;

// A shared `queryOptions()` object so the router's `_authed` beforeLoad
// guard (queryClient.ensureQueryData) and the `useMe()` hook below query the
// exact same cache entry — one auth check, not two racing fetches.
export const meQueryOptions = queryOptions({
  queryKey: ["me"],
  queryFn: async () => {
    const result = await apiClient.GET("/api/v1/me");
    return unwrap(result).data as MeRow;
  },
  retry: false,
});

export function useMe() {
  return useQuery(meQueryOptions);
}

export function useMyCharacters() {
  return useQuery({
    queryKey: ["me", "characters"],
    queryFn: async () => {
      const result = await apiClient.GET("/api/v1/me/characters");
      const body = unwrap(result);
      return { rows: (body.data ?? []) as MeRow[], sync: body._sync };
    },
  });
}
