// The unknown-scope board. No legacy equivalent — it exists to surface
// Principle 14: an ESI scope string is opaque and is recorded verbatim
// however novel its grammar, never rejected or parsed. The board is where
// a human eventually reads what CCP invented.
//
// The acknowledge action is what keeps it finite. Without it the board
// grows without bound and gets ignored, which is the same as not having
// it. Acknowledging writes app.esi_scope.acknowledged_at, and the board
// lists only unacknowledged rows, so the row leaves on the next fetch.
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn } from "@/components/data-table/columns";
import { Button } from "@/components/ui/button";
import { useAcknowledgeScope, unknownScopesPath } from "@/features/admin/queries";

export function UnknownScopes() {
  const { t } = useTranslation();
  const query = useCollection(unknownScopesPath, {}, ["admin", "scopes", "unknown"]);
  const acknowledge = useAcknowledgeScope();

  const columns: ColumnDef<Row>[] = [
    {
      id: "scope",
      accessorKey: "scope",
      header: t("admin.scopes.scope"),
      meta: { className: "font-mono" },
      cell: ({ getValue }) => String(getValue() ?? "—"),
    },
    dateColumn("first_seen_at", t("admin.scopes.firstSeen")),
    {
      id: "acknowledge",
      header: t("admin.scopes.action"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="outline"
          data-testid="acknowledge-scope"
          disabled={acknowledge.isPending}
          onClick={(e) => {
            e.stopPropagation();
            acknowledge.mutate(String(row.original.scope));
          }}
        >
          {t("admin.acknowledge")}
        </Button>
      ),
    },
  ];

  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={t("admin.scopes.heading")}
      getRowId={(r) => String(r.scope)}
    />
  );
}
