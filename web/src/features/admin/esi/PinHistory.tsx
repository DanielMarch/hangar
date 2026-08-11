// Every recorded pin advance, with its route diff rendered rather than
// dumped. `route_diff` is a jsonb column and — since SRS defect B12 was
// closed in this phase — arrives as a nested JSON object, so this reads
// `row.route_diff.newly_blocked` directly. It does NOT decode anything: a
// client that has to decode a field of a HANGAR response is the defect,
// not the workaround (SRS §6).
import { useTranslation } from "react-i18next";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, textColumn } from "@/components/data-table/columns";
import { pinHistoryPath, type RouteDiff } from "@/features/admin/queries";
import type { ColumnDef } from "@tanstack/react-table";

/**
 * Reads the diff off a history row. Returns undefined for a row recorded
 * before this phase, whose diff really is the empty `{}` placeholder
 * AdvancePin used to substitute for a nil argument — those are shown as
 * "not recorded", which is the truth, rather than as "nothing changed",
 * which would be a fabrication.
 */
function readDiff(row: Row): RouteDiff | undefined {
  const raw = row.route_diff;
  if (raw === null || typeof raw !== "object") return undefined;
  const diff = raw as Partial<RouteDiff>;
  if (!Array.isArray(diff.newly_blocked) || !Array.isArray(diff.newly_unblocked)) {
    return undefined;
  }
  return diff as RouteDiff;
}

export function PinHistory() {
  const { t } = useTranslation();
  const query = useCollection(pinHistoryPath, {}, ["admin", "esi", "pin-history"]);

  const columns: ColumnDef<Row>[] = [
    dateColumn("advanced_at", t("admin.esi.pinHistory.advancedAt")),
    textColumn("old_pin", t("admin.esi.pinHistory.oldPin")),
    textColumn("new_pin", t("admin.esi.pinHistory.newPin")),
    textColumn("actor", t("admin.esi.pinHistory.actor")),
    {
      id: "route_diff",
      header: t("admin.esi.pinHistory.diff"),
      cell: ({ row }) => <DiffSummary row={row.original} />,
    },
  ];

  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={t("admin.esi.pinHistoryHeading")}
      getRowId={(r) => String(r.pin_id)}
    />
  );
}

function DiffSummary({ row }: { row: Row }) {
  const { t } = useTranslation();
  const diff = readDiff(row);
  if (!diff) {
    return (
      <span className="text-muted-foreground">
        {t("admin.esi.pinHistory.diffNotRecorded")}
      </span>
    );
  }
  if (diff.newly_blocked.length === 0 && diff.newly_unblocked.length === 0) {
    return (
      <span className="text-muted-foreground">
        {t("admin.pin.noRoutesChange", { count: diff.unchanged })}
      </span>
    );
  }
  return (
    <span className="font-mono text-xs tabular-nums">
      {t("admin.esi.pinHistory.diffSummary", {
        unblocked: diff.newly_unblocked.length,
        blocked: diff.newly_blocked.length,
      })}
    </span>
  );
}
