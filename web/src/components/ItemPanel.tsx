// The single-item counterpart to data-table/CollectionTable.tsx: wires a
// useItem()-shaped query result to the same
// loading/forbidden/unavailable/render states, for detail panels (a
// character sheet, one contract, one mail body) rather than lists.
import { CardSkeleton } from "@/components/Skeleton";
import { Forbidden, isForbidden } from "@/components/PermissionGate";
import { UnavailablePanel } from "@/components/SyncBadge";
import type { Row, Sync } from "@/api/queries/collection";

interface QueryLike {
  isPending: boolean;
  error: unknown;
  data?: { data: Row | null; sync?: Sync };
}

export function ItemPanel({
  query,
  children,
}: {
  query: QueryLike;
  children: (data: Row) => React.ReactNode;
}) {
  if (query.isPending) return <CardSkeleton />;

  if (query.error) {
    if (isForbidden(query.error))
      return <Forbidden detail={query.error.detail} />;
    throw query.error;
  }

  const data = query.data?.data;
  if (!data)
    return <UnavailablePanel reason={query.data?.sync?.blocked_by_pin} />;

  return <>{children(data)}</>;
}
