// Wires a useCollection()/useInfiniteCollection() query result to
// DataTable + SyncBadge + the shared loading/forbidden/unavailable/empty
// states — the second half of "one generic table reused everywhere" (the
// first half, DataTable.tsx, is pure presentation and knows nothing about
// queries, permissions or the sync envelope). Every Phase 17 list screen
// is a thin `columns` array plus a call to this component.
import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";

import { Forbidden, isForbidden } from "@/components/PermissionGate";
import { SyncBadge, UnavailablePanel } from "@/components/SyncBadge";
import { TableSkeleton } from "@/components/Skeleton";
import { DataTable } from "@/components/data-table/DataTable";
import { DataTableToolbar } from "@/components/data-table/filters";
import type { Row, Sync } from "@/api/queries/collection";

interface QueryLike {
  isPending: boolean;
  error: unknown;
  data?: { rows: Row[] | null; sync?: Sync };
}

export function CollectionTable({
  query,
  columns,
  title,
  onRowClick,
  toolbarExtra,
  getRowId,
}: {
  query: QueryLike;
  columns: ColumnDef<Row>[];
  title?: string;
  onRowClick?: (row: Row) => void;
  toolbarExtra?: React.ReactNode;
  getRowId?: (row: Row, index: number) => string;
}) {
  const [globalFilter, setGlobalFilter] = useState("");

  if (query.isPending) return <TableSkeleton />;

  if (query.error) {
    if (isForbidden(query.error))
      return <Forbidden detail={query.error.detail} />;
    // Anything else is a real failure — let the nearest ErrorBoundary
    // (SRS §8.3: "every distinct data module wrapped in an error boundary")
    // render its local retry rather than this component special-casing it.
    throw query.error;
  }

  const rows = query.data?.rows ?? null;
  const sync = query.data?.sync;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        {title ? (
          <h3 className="text-sm font-medium text-muted-foreground">{title}</h3>
        ) : (
          <span />
        )}
        {sync && <SyncBadge sync={sync} />}
      </div>

      {rows === null ? (
        <UnavailablePanel reason={sync?.blocked_by_pin} />
      ) : (
        <>
          <DataTableToolbar
            globalFilter={globalFilter}
            onGlobalFilterChange={setGlobalFilter}
            extra={toolbarExtra}
          />
          <DataTable
            columns={columns}
            data={rows}
            globalFilter={globalFilter}
            onRowClick={onRowClick}
            getRowId={getRowId}
          />
        </>
      )}
    </div>
  );
}
