// The exposure board: users whose actual platform groups disagree with
// their entitlements, and the revocations that have been enqueued but not
// yet confirmed by the platform.
//
// AGES ARE COMPUTED FROM `event_at`, NOT FROM JOB START. That is the whole
// point of the column and a Phase 18 exit criterion. Gate 2 measures
// revocation latency as `platform_call_completed_at - event_at` — from the
// moment the triggering condition became TRUE, not from the moment a
// worker got round to it — so a board that aged rows from job start would
// under-report exactly the exposure the gate exists to bound, and would do
// so worst precisely when the queue is backed up.
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";

import type { Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, textColumn } from "@/components/data-table/columns";
import { Badge } from "@/components/ui/badge";
import {
  ageSeconds,
  formatAge,
  REVOCATION_SLO_SECONDS,
} from "@/features/admin/provisioning/age";
import { useExposureBoard } from "@/features/admin/queries";

export function ExposureBoard({ platformId }: { platformId: string }) {
  const { t } = useTranslation();
  const query = useExposureBoard(platformId);

  // An age is only exact if it keeps moving. A value rendered once and
  // left alone is stale the second after it paints, which on a board whose
  // job is to show how long an exposure has lasted is the same defect as
  // measuring from the wrong instant.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  const columns: ColumnDef<Row>[] = [
    {
      id: "exposure_kind",
      accessorKey: "exposure_kind",
      header: t("admin.exposures.kind"),
      cell: ({ getValue }) =>
        getValue() === "pending" ? (
          <Badge variant="outline">{t("admin.exposures.pending")}</Badge>
        ) : (
          <Badge variant="destructive">{t("admin.exposures.mismatched")}</Badge>
        ),
    },
    textColumn("user_id", t("admin.exposures.user")),
    textColumn("action", t("admin.exposures.action")),
    textColumn("reason", t("admin.exposures.reason")),
    dateColumn("event_at", t("admin.exposures.eventAt")),
    {
      id: "age",
      header: t("admin.exposures.age"),
      meta: { className: "font-mono tabular-nums text-right" },
      cell: ({ row }) => <AgeCell row={row.original} now={now} />,
    },
  ];

  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={t("admin.exposures.heading")}
      getRowId={(r, i) => String(r.audit_id ?? `${String(r.user_id)}:${i}`)}
    />
  );
}

function AgeCell({ row, now }: { row: Row; now: number }) {
  const { t } = useTranslation();
  // Deliberately `event_at` and nothing else. A mismatched-state row has
  // no event_at (it is a live desired-vs-actual disagreement, not an
  // enqueued action), and shows "—" rather than being aged from
  // last_reconciled_at, which would answer a different question.
  const age = ageSeconds(row.event_at, now);
  if (age === null) {
    return <span className="text-muted-foreground">{t("admin.exposures.noAge")}</span>;
  }
  const label = formatAge(age);
  return age > REVOCATION_SLO_SECONDS ? (
    <Badge variant="destructive" data-testid="exposure-age-breach">
      {label}
    </Badge>
  ) : (
    <span data-testid="exposure-age">{label}</span>
  );
}
