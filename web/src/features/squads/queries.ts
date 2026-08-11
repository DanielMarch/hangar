// There is no GET /api/v1/squads/{id} single-squad detail route — only
// list-squads (GET /api/v1/squads, "squads.view") returns every squad's
// name/type/description. A detail screen finds "its" squad from that
// cached list rather than a dedicated fetch; `squadsListQueryOptions` is
// the one place that list is defined so every caller shares the same
// cache entry (the router's `_authed/squads/$squadId` loader,
// `useMyCharacters`-style hooks in the tab components, and the index
// list) instead of racing separate fetches.
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { apiClient, unwrap } from "@/api/client";
import { collectionQueryOptions, type Row } from "@/api/queries/collection";

export const squadsListQueryOptions = () =>
  collectionQueryOptions("/api/v1/squads", {}, ["squads"]);

export const squadMembersPath = "/api/v1/squads/{id}/members" as const;
export const squadModeratorsPath = "/api/v1/squads/{id}/moderators" as const;
export const squadRolesPath = "/api/v1/squads/{id}/roles" as const;
export const squadApplicationsPath =
  "/api/v1/squads/{id}/applications" as const;

export function findSquad(
  rows: Row[] | null | undefined,
  squadId: string,
): Row | undefined {
  return rows?.find((r) => r.squad_id === squadId);
}

export function useResolveApplication(squadId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: { applicationId: string; approve: boolean }) => {
      const result = await apiClient.POST(
        "/api/v1/squads/{id}/applications/resolve",
        {
          params: { path: { id: squadId } },
          body: { application_id: input.applicationId, approve: input.approve },
        },
      );
      return unwrap(result);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["squads", squadId, "applications"],
      });
    },
  });
}
