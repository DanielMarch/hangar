// POST /api/v1/support/search — the only way to reach a character or
// corporation this session hasn't already linked (there is no bare GET
// /api/v1/characters or /api/v1/corporations list route; SRS §6.7 gates
// this on the caller having a resolved acting character). Used by the
// characters/corporations index screens' "find someone" box.
import { useMutation } from "@tanstack/react-query";

import { apiClient, unwrap } from "@/api/client";

export interface SearchResult {
  kind: "character" | "corporation" | "alliance";
  id: number;
  name: string;
}

export function useEntitySearch() {
  return useMutation({
    mutationFn: async (input: { query: string; kinds?: string[] }) => {
      const result = await apiClient.POST("/api/v1/support/search", {
        body: { query: input.query, kinds: input.kinds },
      });
      const body = unwrap(result);
      return (body.data ?? []) as unknown as SearchResult[];
    },
  });
}
