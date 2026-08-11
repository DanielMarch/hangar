// Dashboard ("/"). SRS §6.7 / Phase 15.1: esi-status (ESI's own health) and
// server-status (Tranquility players/VIP/version) are distinct endpoints —
// rendered as two separate cards, never merged. server-status is
// `blocked_by_pin`-shaped until its first sync completes; SyncBadge is what
// tells the member that, so this component never has to special-case "0
// players online" itself.
import { createFileRoute } from "@tanstack/react-router";
import { Suspense } from "react";
import { useTranslation } from "react-i18next";

import { useEsiStatus, useServerStatus } from "@/api/queries/status";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { CardSkeleton } from "@/components/Skeleton";
import { SyncBadge } from "@/components/SyncBadge";
import { Badge } from "@/components/ui/badge";

export const Route = createFileRoute("/_authed/")({
  staticData: { breadcrumbKey: "nav.dashboard" },
  component: Dashboard,
});

function Dashboard() {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-xl font-semibold">{t("dashboard.heading")}</h1>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <ErrorBoundary>
          <Suspense fallback={<CardSkeleton />}>
            <ServerStatusCard />
          </Suspense>
        </ErrorBoundary>
        <ErrorBoundary>
          <Suspense fallback={<CardSkeleton />}>
            <EsiStatusCard />
          </Suspense>
        </ErrorBoundary>
      </div>
    </div>
  );
}

function ServerStatusCard() {
  const { t } = useTranslation();
  const { data } = useServerStatus();

  return (
    <div className="space-y-3 rounded-lg border border-border bg-card p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium text-muted-foreground">
          {t("dashboard.serverStatus")}
        </h2>
        <SyncBadge sync={data.sync} />
      </div>
      {data.data ? (
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-muted-foreground">{t("dashboard.players")}</dt>
          <dd className="font-mono tabular-nums">
            {String(data.data.player_count ?? "—")}
          </dd>
          <dt className="text-muted-foreground">{t("dashboard.vip")}</dt>
          <dd className="font-mono tabular-nums">
            {String(data.data.vip ?? false)}
          </dd>
          <dt className="text-muted-foreground">{t("dashboard.version")}</dt>
          <dd className="font-mono tabular-nums">
            {String(data.data.server_version ?? "—")}
          </dd>
        </dl>
      ) : (
        <p className="text-sm text-muted-foreground">
          {data.sync.blocked_by_pin}
        </p>
      )}
    </div>
  );
}

function EsiStatusCard() {
  const { t } = useTranslation();
  const { data } = useEsiStatus();
  const healthy = data.data?.healthy === true;

  return (
    <div className="space-y-3 rounded-lg border border-border bg-card p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium text-muted-foreground">
          {t("dashboard.esiStatus")}
        </h2>
        <SyncBadge sync={data.sync} />
      </div>
      <Badge variant={healthy ? "secondary" : "destructive"}>
        {healthy ? t("dashboard.healthy") : t("dashboard.degraded")}
      </Badge>
    </div>
  );
}
