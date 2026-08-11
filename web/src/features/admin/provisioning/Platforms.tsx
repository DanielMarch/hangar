// Access-provisioning platforms: the list, and one platform's detail
// (lockdown, rule editor, exposure board).
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";
import { useQuery } from "@tanstack/react-query";

import type { Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { boolColumn, textColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ExposureBoard } from "@/features/admin/provisioning/ExposureBoard";
import { RuleEditor } from "@/features/admin/provisioning/RuleEditor";
import { platformsQueryOptions, useLockdown } from "@/features/admin/queries";

export function Platforms() {
  const { t } = useTranslation();
  const query = useQuery(platformsQueryOptions());
  const [selected, setSelected] = useState<string | null>(null);

  const columns: ColumnDef<Row>[] = [
    textColumn("name", t("admin.platforms.name")),
    textColumn("kind", t("admin.platforms.kind")),
    boolColumn("enabled", t("admin.platforms.enabled")),
    {
      id: "locked_down",
      accessorKey: "locked_down",
      header: t("admin.platforms.lockdown"),
      cell: ({ getValue }) =>
        getValue() ? (
          <Badge variant="destructive">{t("admin.platforms.lockedDown")}</Badge>
        ) : (
          <Badge variant="secondary">{t("admin.platforms.live")}</Badge>
        ),
    },
  ];

  const platforms = query.data?.rows ?? [];
  const current = platforms.find((p) => String(p.platform_id) === selected);

  return (
    <div className="space-y-6">
      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.platforms.heading")}
        onRowClick={(row) => setSelected(String(row.platform_id))}
        getRowId={(r) => String(r.platform_id)}
      />

      {selected && current && (
        <div className="space-y-6 rounded-lg border border-border p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">{String(current.name)}</h2>
            <LockdownControl
              platformId={selected}
              lockedDown={Boolean(current.locked_down)}
            />
          </div>
          <ErrorBoundary>
            <RuleEditor platformId={selected} />
          </ErrorBoundary>
          <ErrorBoundary>
            <ExposureBoard platformId={selected} />
          </ErrorBoundary>
        </div>
      )}
    </div>
  );
}

/**
 * The incident freeze. Distinct from `enabled` (which is "is this platform
 * in use at all") — lockdown means "stop all outbound provisioning right
 * now", and it records who froze it and why, so the reason field is
 * mandatory on the way in and ignored on the way out.
 */
function LockdownControl({
  platformId,
  lockedDown,
}: {
  platformId: string;
  lockedDown: boolean;
}) {
  const { t } = useTranslation();
  const lockdown = useLockdown(platformId);
  const [reason, setReason] = useState("");

  return (
    <div className="flex flex-wrap items-center gap-2">
      {!lockedDown && (
        <input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t("admin.platforms.reasonPlaceholder")}
          aria-label={t("admin.platforms.reasonPlaceholder")}
          className="h-8 w-56 rounded-md border border-border bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
        />
      )}
      <Button
        size="sm"
        variant={lockedDown ? "outline" : "destructive"}
        disabled={lockdown.isPending || (!lockedDown && reason.trim() === "")}
        onClick={() =>
          lockdown.mutate({ lockedDown: !lockedDown, reason: reason.trim() })
        }
        data-testid="lockdown-toggle"
      >
        {lockedDown
          ? t("admin.platforms.unlock")
          : t("admin.platforms.lock")}
      </Button>
    </div>
  );
}
