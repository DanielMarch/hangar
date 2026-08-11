// The two alerting boards: dead-lettered deliveries (with requeue) and
// unrecognised notification types (with acknowledge).
//
// Same reasoning as the unknown-scope board — an unrecognised notification
// type is recorded rather than dropped (Principle 14), and acknowledging
// is what stops the board growing without bound. `sample_payload` is a
// jsonb column and is rendered as formatted JSON, which only became
// possible once defect B12 was closed: it is one of the three NULLABLE
// jsonb columns sqlc was generating as plain []byte, so it reached the
// wire hex-encoded even after the converter fix.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, numberColumn, textColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  deadLetterPath,
  unknownTypesPath,
  useAcknowledgeNotificationType,
  useRequeueDeadLetter,
} from "@/features/admin/queries";

export function AlertBoards() {
  return (
    <div className="space-y-8">
      <ErrorBoundary>
        <DeadLetterBoard />
      </ErrorBoundary>
      <ErrorBoundary>
        <UnknownTypesBoard />
      </ErrorBoundary>
    </div>
  );
}

function DeadLetterBoard() {
  const { t } = useTranslation();
  const query = useCollection(deadLetterPath, {}, ["admin", "alerts", "dead-letter"]);
  const requeue = useRequeueDeadLetter();

  const columns: ColumnDef<Row>[] = [
    dateColumn("created_at", t("admin.alerts.createdAt")),
    textColumn("alert_type", t("admin.alerts.alertType")),
    textColumn("channel_kind", t("admin.alerts.channel")),
    textColumn("channel_name", t("admin.alerts.channelName")),
    numberColumn("attempts", t("admin.alerts.attempts")),
    dateColumn("last_attempt_at", t("admin.alerts.lastAttempt")),
    textColumn("error", t("admin.alerts.lastError")),
    {
      id: "requeue",
      header: t("admin.alerts.action"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="outline"
          disabled={requeue.isPending}
          data-testid="requeue-delivery"
          onClick={(e) => {
            e.stopPropagation();
            requeue.mutate(String(row.original.delivery_id));
          }}
        >
          {t("admin.alerts.requeue")}
        </Button>
      ),
    },
  ];

  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={t("admin.alerts.deadLetterHeading")}
      getRowId={(r, i) => String(r.delivery_id ?? i)}
    />
  );
}

function UnknownTypesBoard() {
  const { t } = useTranslation();
  const query = useCollection(unknownTypesPath, {}, ["admin", "alerts", "unknown-types"]);
  const acknowledge = useAcknowledgeNotificationType();
  const [sample, setSample] = useState<Row | null>(null);

  const columns: ColumnDef<Row>[] = [
    {
      id: "type",
      accessorKey: "type",
      header: t("admin.alerts.type"),
      meta: { className: "font-mono" },
      cell: ({ getValue }) => String(getValue() ?? "—"),
    },
    dateColumn("first_seen_at", t("admin.alerts.firstSeen")),
    dateColumn("last_seen_at", t("admin.alerts.lastSeen")),
    numberColumn("occurrences", t("admin.alerts.occurrences")),
    {
      id: "sample",
      header: t("admin.alerts.sample"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="ghost"
          onClick={(e) => {
            e.stopPropagation();
            setSample(row.original);
          }}
        >
          {t("admin.alerts.viewSample")}
        </Button>
      ),
    },
    {
      id: "acknowledge",
      header: t("admin.alerts.action"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="outline"
          disabled={acknowledge.isPending}
          data-testid="acknowledge-type"
          onClick={(e) => {
            e.stopPropagation();
            acknowledge.mutate(String(row.original.type));
          }}
        >
          {t("admin.acknowledge")}
        </Button>
      ),
    },
  ];

  return (
    <>
      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.alerts.unknownTypesHeading")}
        getRowId={(r) => String(r.type)}
      />
      <Dialog open={sample !== null} onOpenChange={() => setSample(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{sample ? String(sample.type) : ""}</DialogTitle>
          </DialogHeader>
          {sample?.sample_payload == null ? (
            <p className="text-sm text-muted-foreground">
              {t("admin.alerts.noSample")}
            </p>
          ) : (
            <pre className="max-h-[60vh] overflow-auto rounded-md border border-border bg-background p-3 font-mono text-xs">
              {JSON.stringify(sample?.sample_payload, null, 2)}
            </pre>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
