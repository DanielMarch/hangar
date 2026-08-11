// Generic query hooks reused by every Phase 17 list/detail screen — the
// roadmap's explicit alternative to Phase 16-legacy SeAT's "one bespoke
// controller + DataTable class per screen" pattern (72 controllers). Every
// HANGAR collection response is structurally `{ data, page, _sync }` and
// every item response `{ data, _sync }` regardless of which named OpenAPI
// schema wraps it (CollectionMapStringInterface, ItemMapStringInterface,
// MeOutBody, ...) — Huma's dto.Row()-based handlers guarantee this (see
// internal/api/dto/row.go, internal/api/envelope.go) — so one hook per
// shape, parameterised over the path, covers the whole surface.
//
// `blocked_by_pin` (Sync.BlockedByPin) means `data` is `null`, not `[]` —
// these hooks preserve that distinction (no `?? []` coercion) so callers
// can render UNAVAILABLE instead of an empty table (SRS §8.3).
//
// `any` below is a deliberate, file-scoped exception: openapi-fetch's
// `GET` is typed as an overloaded function keyed on literal path strings,
// which is exactly right for a single call site but cannot be expressed
// generically over `Path extends GetPaths` without losing the type safety
// these hooks exist to give their callers (`columns.ts`, every feature
// screen) in the first place — the cast is contained to this file.
/* eslint-disable @typescript-eslint/no-explicit-any */
import {
  queryOptions,
  useInfiniteQuery,
  useQuery,
} from "@tanstack/react-query";
import type { FetchOptions } from "openapi-fetch";

import { apiClient, unwrap } from "@/api/client";
import type { components, paths } from "@/api/schema.d.ts";

export type Row = Record<string, unknown>;
export type Sync = components["schemas"]["Sync"];

type GetPaths = {
  [K in keyof paths]: paths[K] extends { get: unknown } ? K : never;
}[keyof paths];

interface CollectionBody {
  data: Row[] | null;
  page?: { next_cursor?: string; prev_cursor?: string; limit?: number };
  _sync: Sync;
}

interface ItemBody {
  data: Row | null;
  _sync: Sync;
}

/**
 * One page of a cursor-paginated collection. `path` must be a GET route;
 * `init` carries `{ params: { path, query } }` exactly as openapi-fetch
 * expects. Cursors are opaque — callers pass `page.next_cursor` straight
 * back as `query.after`, never construct or parse one (SRS §6, the "'0' is
 * the start/end sentinel" rule).
 */
export function collectionQueryOptions<Path extends GetPaths>(
  path: Path,
  init: FetchOptions<paths[Path]["get"]>,
  queryKey: readonly unknown[],
) {
  return queryOptions({
    queryKey,
    queryFn: async () => {
      const result = await (apiClient.GET as any)(path, init);
      const body = unwrap(result) as CollectionBody;
      return { rows: body.data, sync: body._sync, page: body.page };
    },
  });
}

export function useCollection<Path extends GetPaths>(
  path: Path,
  init: FetchOptions<paths[Path]["get"]>,
  queryKey: readonly unknown[],
  enabled = true,
) {
  return useQuery({ ...collectionQueryOptions(path, init, queryKey), enabled });
}

/**
 * Accumulates every page of a cursor-paginated collection into one growing
 * row array — what DataTable's virtualizer scrolls over for the
 * potentially-100k-row surfaces (wallet journal/transactions). Still never
 * touches the cursor itself; TanStack Query's `getNextPageParam` just
 * threads `page.next_cursor` back in as the next `after`.
 */
export function useInfiniteCollection<Path extends GetPaths>(
  path: Path,
  paramsBase: FetchOptions<paths[Path]["get"]>,
  queryKey: readonly unknown[],
  enabled = true,
) {
  const query = useInfiniteQuery({
    queryKey,
    enabled,
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) => {
      const init = pageParam
        ? ({
            ...paramsBase,
            params: {
              ...(paramsBase as any).params,
              query: { ...(paramsBase as any).params?.query, after: pageParam },
            },
          } as any)
        : paramsBase;
      const result = await (apiClient.GET as any)(path, init);
      const body = unwrap(result) as CollectionBody;
      return { rows: body.data, sync: body._sync, page: body.page };
    },
    getNextPageParam: (lastPage) => {
      const next = lastPage.page?.next_cursor;
      return next && next !== "0" ? next : undefined;
    },
  });

  const pages = query.data?.pages ?? [];
  const anyBlocked = pages.some((p) => p.sync.blocked_by_pin);
  const rows = anyBlocked ? null : pages.flatMap((p) => p.rows ?? []);

  return {
    ...query,
    rows,
    sync: pages[0]?.sync,
  };
}

export function itemQueryOptions<Path extends GetPaths>(
  path: Path,
  init: FetchOptions<paths[Path]["get"]>,
  queryKey: readonly unknown[],
) {
  return queryOptions({
    queryKey,
    queryFn: async () => {
      const result = await (apiClient.GET as any)(path, init);
      const body = unwrap(result) as ItemBody;
      return { data: body.data, sync: body._sync };
    },
  });
}

export function useItem<Path extends GetPaths>(
  path: Path,
  init: FetchOptions<paths[Path]["get"]>,
  queryKey: readonly unknown[],
  enabled = true,
) {
  return useQuery({ ...itemQueryOptions(path, init, queryKey), enabled });
}
