// The ONE generic, column-driven table every Phase 17 (and later) screen
// reuses — SRS §8.3 / roadmap: "Seventy bespoke tables is the legacy
// failure mode being replaced." @tanstack/react-table drives columns/rows;
// @tanstack/react-virtual (virtualization.ts) virtualizes the body so a
// 100k-row wallet journal scrolls at 60fps without mounting 100k DOM rows.
//
// Rendered as ARIA grid semantics over <div>s, not a native <table> — a
// virtualized <tr> cannot be absolutely positioned inside a real <tbody>
// without breaking column alignment, which is exactly the "fixed row
// height, never variable" constraint this component exists to hold to.
// This is the same pattern TanStack's own virtualized-table examples use.
//
// Complex tables scroll horizontally within their own container
// (`overflow-x-auto` on the scroll element below), never on the app shell
// (SRS §8.2 mobile-responsiveness rule) — DataTable's root never exceeds
// `w-full`.
import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  useReactTable,
  type ColumnDef,
} from "@tanstack/react-table";
import { useRef } from "react";
import { useTranslation } from "react-i18next";

import {
  ROW_HEIGHT_PX,
  useRowVirtualizer,
} from "@/components/data-table/virtualization";
import { cn } from "@/lib/utils";

declare module "@tanstack/react-table" {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars -- module augmentation must match react-table's own generic signature
  interface ColumnMeta<TData, TValue> {
    className?: string;
  }
}

interface DataTableProps<T> {
  columns: ColumnDef<T>[];
  data: T[];
  globalFilter?: string;
  onRowClick?: (row: T) => void;
  getRowId?: (row: T, index: number) => string;
  className?: string;
  /** Caps the scroll area height; the table still virtualizes beyond it. */
  maxHeightClassName?: string;
}

export function DataTable<T>({
  columns,
  data,
  globalFilter,
  onRowClick,
  getRowId,
  className,
  maxHeightClassName = "max-h-[70vh]",
}: DataTableProps<T>) {
  const { t } = useTranslation();
  const scrollRef = useRef<HTMLDivElement>(null);

  const table = useReactTable({
    data,
    columns,
    state: { globalFilter },
    getRowId,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
  });

  const rows = table.getRowModel().rows;
  const virtualizer = useRowVirtualizer(rows.length, scrollRef);
  const virtualRows = virtualizer.getVirtualItems();
  const totalHeight = virtualizer.getTotalSize();
  const paddingTop = virtualRows.length > 0 ? virtualRows[0].start : 0;
  const paddingBottom =
    virtualRows.length > 0
      ? totalHeight - virtualRows[virtualRows.length - 1].end
      : 0;

  if (data.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        {t("dataTable.noResults")}
      </p>
    );
  }

  return (
    <div
      ref={scrollRef}
      role="table"
      className={cn(
        "w-full overflow-x-auto overflow-y-auto rounded-md border border-border",
        maxHeightClassName,
        className,
      )}
    >
      <div
        role="rowgroup"
        className="sticky top-0 z-10 flex min-w-full border-b border-border bg-card"
      >
        {table.getHeaderGroups().map((headerGroup) =>
          headerGroup.headers.map((header) => (
            <div
              key={header.id}
              role="columnheader"
              className={cn(
                "flex-1 truncate px-3 py-2 text-left text-xs font-medium text-muted-foreground select-none",
                header.column.columnDef.meta?.className,
              )}
              style={{ minWidth: 96 }}
            >
              {flexRender(header.column.columnDef.header, header.getContext())}
            </div>
          )),
        )}
      </div>

      <div
        role="rowgroup"
        style={{ height: totalHeight, position: "relative" }}
        className="min-w-full"
      >
        <div style={{ height: paddingTop }} />
        {virtualRows.map((virtualRow) => {
          const row = rows[virtualRow.index];
          return (
            <div
              key={row.id}
              role="row"
              data-index={virtualRow.index}
              onClick={onRowClick ? () => onRowClick(row.original) : undefined}
              className={cn(
                "flex border-b border-border/60",
                onRowClick && "cursor-pointer hover:bg-accent/50",
              )}
              style={{ height: ROW_HEIGHT_PX }}
            >
              {row.getVisibleCells().map((cell) => (
                <div
                  key={cell.id}
                  role="cell"
                  className={cn(
                    "flex flex-1 items-center truncate px-3 text-sm",
                    cell.column.columnDef.meta?.className,
                  )}
                  style={{ minWidth: 96 }}
                >
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </div>
              ))}
            </div>
          );
        })}
        <div style={{ height: paddingBottom }} />
      </div>
    </div>
  );
}
