// The ingested ESI route catalogue (app.esi_route), one row per operation.
//
// `spec_fragment` and `identifier_types` are both jsonb and both excluded
// from the table: the fragment is a whole OpenAPI operation object and the
// identifier map is a lookup, neither of which belongs in a cell. They are
// rendered in the detail panel instead, as formatted JSON — which is only
// possible because they now arrive as nested JSON rather than hex (SRS §6,
// defect B12 closed in this phase).
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { boolColumn, textColumn } from "@/components/data-table/columns";
import { syncRoutesPath } from "@/features/admin/queries";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export function RouteCatalogue() {
  const { t } = useTranslation();
  const query = useCollection(syncRoutesPath, {}, ["admin", "sync", "routes"]);
  const [selected, setSelected] = useState<Row | null>(null);

  const columns: ColumnDef<Row>[] = [
    textColumn("method", t("admin.routes.method")),
    textColumn("upstream_path", t("admin.routes.path")),
    textColumn("operation_id", t("admin.routes.operationId")),
    textColumn("compatibility_date", t("admin.routes.compatibilityDate")),
    boolColumn("blocked_by_pin", t("admin.routes.blocked")),
    textColumn("cache_mode", t("admin.routes.cacheMode")),
    textColumn("rate_limit_group", t("admin.routes.rateLimitGroup")),
    textColumn("pagination_style", t("admin.routes.pagination")),
  ];

  return (
    <>
      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.routes.heading")}
        onRowClick={setSelected}
        getRowId={(r) => String(r.route_id)}
      />
      <Dialog open={selected !== null} onOpenChange={() => setSelected(null)}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>
              {selected
                ? `${String(selected.method)} ${String(selected.upstream_path)}`
                : ""}
            </DialogTitle>
          </DialogHeader>
          {selected && (
            <div className="max-h-[60vh] space-y-4 overflow-y-auto">
              <JsonBlock
                title={t("admin.routes.identifierTypes")}
                value={selected.identifier_types}
              />
              <JsonBlock
                title={t("admin.routes.specFragment")}
                value={selected.spec_fragment}
              />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function JsonBlock({ title, value }: { title: string; value: unknown }) {
  const { t } = useTranslation();
  return (
    <section className="space-y-1">
      <h4 className="text-xs font-medium text-muted-foreground">{title}</h4>
      {value === null || value === undefined ? (
        <p className="text-sm text-muted-foreground">{t("admin.routes.noValue")}</p>
      ) : (
        <pre className="overflow-x-auto rounded-md border border-border bg-background p-3 font-mono text-xs">
          {JSON.stringify(value, null, 2)}
        </pre>
      )}
    </section>
  );
}
