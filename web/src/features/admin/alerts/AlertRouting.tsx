// §4.4's configuration screen: the alert catalogue, the channel registry,
// and the routing rules that connect them.
//
// ── PHASE 23 (N-4): WHY THIS SCREEN DID NOT EXIST ────────────────────────
//
// Eight generated store queries had no production caller, and two RBAC
// permissions — `alerting.channels.manage` and `alerting.routing.manage` —
// had been in the closed vocabulary since Phase 10 with no endpoint behind
// either. Until N-9 landed in this same phase, a stock installation
// delivered no alerts at all, so "an operator cannot say where alerts go"
// was academic. It is not any more.
//
// ── THE ONE THING THIS SCREEN EXISTS TO MAKE OBVIOUS ─────────────────────
//
// An alert type with NO routing rule produces events that are recorded and
// delivered to nobody. That is the default state of every fresh
// installation — ensureDefaultAlertChannels creates channels from the
// environment and deliberately creates no rules, because who receives what
// is an operator decision — so "unrouted" is the normal starting condition
// rather than an error, and it has to be visible as a fact rather than
// inferred from an empty list.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, textColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  alertChannelsPath,
  alertTypesPath,
  useAlertType,
  useCreateAlertChannel,
  useCreateRoutingRule,
  useSetAlertChannelEnabled,
  type RoutingRuleDraft,
} from "@/features/admin/queries";

/** app.alert_routing_rule.target_kind's CHECK constraint. */
const TARGET_KINDS = [
  "user",
  "squad",
  "corporation",
  "alliance",
  "installation",
] as const;

/** channels.KnownKinds(). */
const CHANNEL_KINDS = ["smtp", "slack_webhook", "discord_webhook"] as const;

export function AlertRouting() {
  return (
    <div className="space-y-8">
      <ErrorBoundary>
        <AlertChannels />
      </ErrorBoundary>
      <ErrorBoundary>
        <AlertCatalogue />
      </ErrorBoundary>
    </div>
  );
}

function AlertChannels() {
  const { t } = useTranslation();
  const query = useCollection(alertChannelsPath, {}, [
    "admin",
    "alerts",
    "channels",
  ]);
  const setEnabled = useSetAlertChannelEnabled();
  const [creating, setCreating] = useState(false);

  const columns: ColumnDef<Row>[] = [
    textColumn("name", t("admin.alerts.channelName")),
    textColumn("kind", t("admin.alerts.channel")),
    {
      id: "enabled",
      accessorKey: "enabled",
      header: t("admin.alerts.enabled"),
      cell: ({ getValue }) => (getValue() ? t("common.yes") : t("common.no")),
    },
    dateColumn("created_at", t("admin.alerts.createdAt")),
    {
      id: "toggle",
      header: t("admin.scopes.action"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="outline"
          data-testid="toggle-channel"
          disabled={setEnabled.isPending}
          onClick={(e) => {
            e.stopPropagation();
            setEnabled.mutate({
              id: String(row.original.channel_id),
              enabled: !row.original.enabled,
            });
          }}
        >
          {row.original.enabled
            ? t("admin.alerts.disable")
            : t("admin.alerts.enable")}
        </Button>
      ),
    },
  ];

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-medium">
          {t("admin.alerts.channelsHeading")}
        </h2>
        <Button
          size="sm"
          data-testid="new-channel"
          onClick={() => setCreating(true)}
        >
          {t("admin.alerts.newChannel")}
        </Button>
      </div>
      {/* A channel's config holds credentials — a webhook URL IS the
          credential — so the server redacts it and the table never has a
          column for it. */}
      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.alerts.channelsHeading")}
        getRowId={(r) => String(r.channel_id)}
      />
      <NewChannelDialog open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}

function NewChannelDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const create = useCreateAlertChannel();
  const [kind, setKind] = useState<string>(CHANNEL_KINDS[1]);
  const [name, setName] = useState("");
  const [config, setConfig] = useState('{"url": ""}');
  const [error, setError] = useState("");

  function submit() {
    setError("");
    let parsed: unknown;
    try {
      parsed = JSON.parse(config);
    } catch {
      // Parsed here as well as on the server, and this is not duplicated
      // validation: the SERVER validates that the configuration BUILDS a
      // working channel (it runs channels.New on it), which is a different
      // and stronger claim than "this is JSON". Catching malformed JSON in
      // the browser just means the operator is told which of the two
      // problems they have.
      setError(t("admin.alerts.configNotJson"));
      return;
    }
    create.mutate(
      { kind, name, config: parsed },
      {
        onSuccess: () => {
          setName("");
          onClose();
        },
        onError: (err: unknown) => setError(String(err)),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("admin.alerts.newChannel")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <label className="block text-sm">
            {t("admin.alerts.channel")}
            <select
              className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1 text-sm"
              value={kind}
              data-testid="channel-kind"
              onChange={(e) => setKind(e.target.value)}
            >
              {CHANNEL_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            {t("admin.alerts.channelName")}
            <Input
              className="mt-1"
              value={name}
              data-testid="channel-name"
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="block text-sm">
            {t("admin.alerts.channelConfig")}
            <textarea
              className="mt-1 h-28 w-full rounded-md border border-border bg-background p-2 font-mono text-xs"
              value={config}
              data-testid="channel-config"
              onChange={(e) => setConfig(e.target.value)}
            />
          </label>
          {error !== "" && (
            <p className="text-sm text-red-400" data-testid="channel-error">
              {error}
            </p>
          )}
          <Button
            data-testid="create-channel"
            disabled={create.isPending || name === ""}
            onClick={submit}
          >
            {t("admin.alerts.create")}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function AlertCatalogue() {
  const { t } = useTranslation();
  const query = useCollection(alertTypesPath, {}, ["admin", "alerts", "types"]);
  const [selected, setSelected] = useState("");

  const columns: ColumnDef<Row>[] = [
    textColumn("alert_type", t("admin.alerts.alertType")),
    textColumn("domain", t("admin.alerts.domain")),
    textColumn("severity", t("admin.alerts.severity")),
    {
      id: "routing",
      header: t("admin.alerts.routing"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="outline"
          data-testid="edit-routing"
          onClick={(e) => {
            e.stopPropagation();
            setSelected(String(row.original.alert_type));
          }}
        >
          {t("admin.alerts.routing")}
        </Button>
      ),
    },
  ];

  return (
    <div className="space-y-2">
      <h2 className="text-lg font-medium">
        {t("admin.alerts.catalogueHeading")}
      </h2>
      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.alerts.catalogueHeading")}
        getRowId={(r) => String(r.alert_type)}
      />
      <RoutingDialog alertType={selected} onClose={() => setSelected("")} />
    </div>
  );
}

function RoutingDialog({
  alertType,
  onClose,
}: {
  alertType: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const detail = useAlertType(alertType);
  const create = useCreateRoutingRule(alertType);
  const channels = useCollection(alertChannelsPath, {}, [
    "admin",
    "alerts",
    "channels",
  ]);

  const [draft, setDraft] = useState<RoutingRuleDraft>({
    target_kind: "installation",
    target_ref: "",
    channel_id: "",
    mention: "",
  });
  const [error, setError] = useState("");

  const data = detail.data?.data as Record<string, unknown> | undefined;
  const rules = (data?.routing_rules ?? []) as Record<string, unknown>[];
  const routed = data?.routed === true;
  const channelRows = channels.data?.rows ?? [];

  return (
    <Dialog open={alertType !== ""} onOpenChange={(next) => !next && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{alertType}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {/* The fact the whole screen exists for. An unrouted alert type
              is recorded and delivered to nobody, and that is the DEFAULT
              on a fresh installation rather than a fault. */}
          {detail.isSuccess && !routed && (
            <p
              className="rounded-md border border-amber-600/40 bg-amber-950/30 p-2 text-sm text-amber-300"
              data-testid="unrouted-warning"
            >
              {t("admin.alerts.unrouted")}
            </p>
          )}

          <div>
            <h3 className="mb-1 text-sm font-medium">
              {t("admin.alerts.currentRules")}
            </h3>
            {rules.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {t("admin.alerts.noRules")}
              </p>
            ) : (
              <ul className="space-y-1 text-sm" data-testid="routing-rules">
                {rules.map((rule) => (
                  <li key={String(rule.rule_id)} className="font-mono text-xs">
                    {String(rule.target_kind)}
                    {rule.target_ref
                      ? `:${String(rule.target_ref)}`
                      : ""} → {String(rule.channel_id)}
                    {rule.mention ? ` (${String(rule.mention)})` : ""}
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="space-y-2 border-t border-border pt-3">
            <h3 className="text-sm font-medium">{t("admin.alerts.addRule")}</h3>
            <label className="block text-sm">
              {t("admin.alerts.targetKind")}
              <select
                className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1 text-sm"
                value={draft.target_kind}
                data-testid="target-kind"
                onChange={(e) =>
                  setDraft({ ...draft, target_kind: e.target.value })
                }
              >
                {TARGET_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            </label>
            {/* 'installation' is the whole-installation audience and takes
                no ref; every other kind identifies an entity. The server
                enforces both directions — the schema can only say the
                column is nullable. */}
            {draft.target_kind !== "installation" && (
              <label className="block text-sm">
                {t("admin.alerts.targetRef")}
                <Input
                  className="mt-1"
                  value={draft.target_ref}
                  data-testid="target-ref"
                  onChange={(e) =>
                    setDraft({ ...draft, target_ref: e.target.value })
                  }
                />
              </label>
            )}
            <label className="block text-sm">
              {t("admin.alerts.channel")}
              <select
                className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1 text-sm"
                value={draft.channel_id}
                data-testid="rule-channel"
                onChange={(e) =>
                  setDraft({ ...draft, channel_id: e.target.value })
                }
              >
                <option value="">—</option>
                {channelRows.map((row) => (
                  <option
                    key={String(row.channel_id)}
                    value={String(row.channel_id)}
                  >
                    {String(row.name)} ({String(row.kind)})
                  </option>
                ))}
              </select>
            </label>
            <label className="block text-sm">
              {t("admin.alerts.mention")}
              <Input
                className="mt-1"
                value={draft.mention}
                data-testid="rule-mention"
                onChange={(e) =>
                  setDraft({ ...draft, mention: e.target.value })
                }
              />
            </label>
            {error !== "" && (
              <p className="text-sm text-red-400" data-testid="rule-error">
                {error}
              </p>
            )}
            <Button
              data-testid="create-rule"
              disabled={create.isPending || draft.channel_id === ""}
              onClick={() => {
                setError("");
                create.mutate(draft, {
                  onSuccess: () =>
                    setDraft({ ...draft, target_ref: "", mention: "" }),
                  onError: (err: unknown) => setError(String(err)),
                });
              }}
            >
              {t("admin.alerts.create")}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
