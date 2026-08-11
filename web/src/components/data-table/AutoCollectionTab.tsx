// Thin wrapper around CollectionTable + autoColumns for the long tail of
// list screens that don't warrant a hand-picked column set (Phase 17: 72
// legacy controllers, only the marquee surfaces get bespoke columns — see
// columns.ts's file banner). One `useCollection` result + a row-id key is
// enough to get a working, virtualized, sync-aware table.
import type { paths } from "@/api/schema.d.ts";
import type { FetchOptions } from "openapi-fetch";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { autoColumns } from "@/components/data-table/columns";

type GetPaths = {
  [K in keyof paths]: paths[K] extends { get: unknown } ? K : never;
}[keyof paths];

export function AutoCollectionTab<Path extends GetPaths>({
  path,
  init,
  queryKey,
  title,
  rowIdKey,
  exclude,
}: {
  path: Path;
  init: FetchOptions<paths[Path]["get"]>;
  queryKey: readonly unknown[];
  title: string;
  rowIdKey: string;
  exclude?: string[];
}) {
  const query = useCollection(path, init, queryKey);
  const rows = query.data?.rows;
  const columns = rows && rows[0] ? autoColumns(rows[0], { exclude }) : [];
  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={title}
      getRowId={(r) => String(r[rowIdKey])}
    />
  );
}
