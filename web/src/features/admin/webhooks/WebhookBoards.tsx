// §4.9's two operator views: the dead-letter board and the outbox backlog.
//
// ── PHASE 23 (N-4): WHY THESE DID NOT EXIST ──────────────────────────────
//
// The webhook outbox has had the same guarantee as §4.4's alert queue since
// Phase 19 — at-least-once, then dead-letter — and the ALERTING half has had
// an admin board since Phase 15. events.DeadLetterBoard and
// events.PendingCount were written, tested and unreachable the whole time.
//
// The consequence was specific rather than cosmetic: a webhook subscriber
// that went permanently unreachable produced deliveries that were correctly
// dead-lettered and that nobody could see. A dead-letter queue exists so a
// non-delivery is VISIBLE rather than lost; half of one is neither.
//
// ── NO REQUEUE HERE, DELIBERATELY, UNLIKE THE ALERTING BOARD ─────────────
//
// The alerting board can requeue a dead-lettered delivery because an alert
// is idempotent to re-send — the worst case is a duplicate message a human
// reads twice. A webhook delivery is a signed event to somebody else's
// system, which may have acted on it already, and replaying it a week later
// is a decision only that system's owner can make. Rotating the endpoint or
// re-triggering the source action is the honest path, and inventing a
// replay button because the neighbouring board has one would be the UI
// wagging the contract.
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import {
  dateColumn,
  numberColumn,
  textColumn,
} from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import {
  webhookDeadLetterPath,
  webhookOutboxQueryOptions,
} from "@/features/admin/queries";

export function WebhookBoards() {
  return (
    <div className="space-y-8">
      <ErrorBoundary>
        <OutboxBacklog />
      </ErrorBoundary>
      <ErrorBoundary>
        <WebhookDeadLetter />
      </ErrorBoundary>
    </div>
  );
}

function OutboxBacklog() {
  const { t } = useTranslation();
  const query = useQuery(webhookOutboxQueryOptions());
  const undispatched = (
    query.data?.data as { undispatched?: number } | undefined
  )?.undispatched;

  return (
    <div className="rounded-lg border border-border p-4">
      <h2 className="text-sm font-semibold">
        {t("admin.webhooks.outboxHeading")}
      </h2>
      <p className="mt-2 text-2xl tabular-nums" data-testid="outbox-backlog">
        {undispatched ?? "—"}
      </p>
      <p className="mt-1 text-sm text-muted-foreground">
        {t("admin.webhooks.outboxHelp")}
      </p>
    </div>
  );
}

function WebhookDeadLetter() {
  const { t } = useTranslation();
  const query = useCollection(webhookDeadLetterPath, {}, [
    "admin",
    "webhooks",
    "dead-letter",
  ]);

  const columns: ColumnDef<Row>[] = [
    dateColumn("failed_at", t("admin.webhooks.failedAt")),
    textColumn("url", t("admin.webhooks.endpoint")),
    numberColumn("attempt", t("admin.alerts.attempts")),
    numberColumn("response_status", t("admin.webhooks.status")),
    textColumn("error", t("admin.alerts.lastError")),
  ];

  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={t("admin.webhooks.deadLetterHeading")}
      getRowId={(r) => String(r.delivery_id)}
    />
  );
}
