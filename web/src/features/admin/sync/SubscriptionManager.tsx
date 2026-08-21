// Per-entity subscription management.
//
// ── PHASE 23 (N-4): WHY THIS DID NOT EXIST ───────────────────────────────
//
// SetSyncSubscriptionEnabled and SetSyncNoCacheOptIn have been generated
// since Phase 6 with no production caller — an operator could not disable a
// subscription, or opt one out of §5.4's conditional caching, except by
// writing SQL — and ListRecentSyncRuns had none either, so nothing surfaced
// what a subscription had actually been doing.
//
// It could not be closed by wiring those three. Nothing anywhere returned a
// subscription_id: `/api/v1/admin/sync/subscriptions` lists schedulable
// ROUTES, which is a different thing wearing the same name. The missing
// piece was a fourth query.
//
// ── WHY THIS IS A LOOKUP AND NOT A LIST ──────────────────────────────────
//
// Gate 1 ran against 225,000 subscriptions. A flat table of them is not a
// screen anybody can use; "what is this character subscribed to, and is any
// of it unhealthy" is the question an operator arrives with, so the entity
// is the input rather than something to scroll to.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";

import type { Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import {
  boolColumn,
  dateColumn,
  numberColumn,
  textColumn,
} from "@/components/data-table/columns";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  useEntitySubscriptions,
  usePatchSubscription,
} from "@/features/admin/queries";

const ENTITY_KINDS = [
  "character",
  "corporation",
  "alliance",
  "global",
] as const;

export function SubscriptionManager() {
  const { t } = useTranslation();
  const [kind, setKind] = useState<string>(ENTITY_KINDS[0]);
  const [entityId, setEntityId] = useState("");
  const [submitted, setSubmitted] = useState({ kind: "", id: "" });

  const query = useEntitySubscriptions(submitted.kind, submitted.id);
  const patch = usePatchSubscription();

  const columns: ColumnDef<Row>[] = [
    {
      id: "upstream_path",
      accessorKey: "upstream_path",
      header: t("admin.subscriptions.route"),
      meta: { className: "font-mono text-xs" },
      cell: ({ getValue }) => String(getValue() ?? "—"),
    },
    boolColumn("enabled", t("admin.subscriptions.enabled")),
    boolColumn("opt_in_no_cache", t("admin.subscriptions.noCache")),
    numberColumn("last_status", t("admin.subscriptions.lastStatus")),
    dateColumn("last_success_at", t("admin.subscriptions.lastSuccess")),
    dateColumn("next_due_at", t("admin.subscriptions.nextDue")),
    textColumn("rate_limit_group", t("admin.subscriptions.group")),
    {
      id: "actions",
      header: t("admin.scopes.action"),
      cell: ({ row }) => (
        <div className="flex gap-1">
          {/* Two independent buttons, each sending ONLY its own field.
              The server's body takes pointers so "leave this alone" and
              "set this to false" are different requests; a form that
              always submitted both would disable a subscription every
              time somebody changed its caching. */}
          <Button
            size="sm"
            variant="outline"
            data-testid="toggle-subscription"
            disabled={patch.isPending}
            onClick={(e) => {
              e.stopPropagation();
              patch.mutate({
                id: String(row.original.subscription_id),
                enabled: !row.original.enabled,
              });
            }}
          >
            {row.original.enabled
              ? t("admin.subscriptions.disable")
              : t("admin.subscriptions.enable")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            data-testid="toggle-subscription-cache"
            disabled={patch.isPending}
            onClick={(e) => {
              e.stopPropagation();
              patch.mutate({
                id: String(row.original.subscription_id),
                optInNoCache: !row.original.opt_in_no_cache,
              });
            }}
          >
            {row.original.opt_in_no_cache
              ? t("admin.subscriptions.useCache")
              : t("admin.subscriptions.bypassCache")}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <h2 className="text-lg font-medium">
        {t("admin.subscriptions.heading")}
      </h2>
      <div className="flex flex-wrap items-end gap-2">
        <label className="text-sm">
          {t("admin.subscriptions.entityKind")}
          <select
            className="mt-1 block rounded-md border border-border bg-background px-2 py-1 text-sm"
            value={kind}
            data-testid="subscription-entity-kind"
            onChange={(e) => setKind(e.target.value)}
          >
            {ENTITY_KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          {t("admin.subscriptions.entityId")}
          <Input
            className="mt-1 w-44"
            value={entityId}
            inputMode="numeric"
            data-testid="subscription-entity-id"
            onChange={(e) => setEntityId(e.target.value)}
          />
        </label>
        <Button
          data-testid="subscription-lookup"
          onClick={() => setSubmitted({ kind, id: entityId })}
        >
          {t("actions.search")}
        </Button>
      </div>
      {submitted.id === "" ? (
        <p className="text-sm text-muted-foreground">
          {t("admin.subscriptions.prompt")}
        </p>
      ) : (
        <CollectionTable
          query={query}
          columns={columns}
          title={t("admin.subscriptions.heading")}
          getRowId={(r) => String(r.subscription_id)}
        />
      )}
    </div>
  );
}
