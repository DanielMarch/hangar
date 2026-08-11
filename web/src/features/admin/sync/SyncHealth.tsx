// Sync Health — the catalogue's own vital signs, plus the subscription
// set the planner schedules from.
import { CalendarClock, Layers, Lock, ListChecks } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { ItemPanel } from "@/components/ItemPanel";
import { Badge } from "@/components/ui/badge";
import { syncHealthQueryOptions, syncSubscriptionsPath } from "@/features/admin/queries";

function asCount(v: unknown): string {
  return typeof v === "number" ? String(v) : "—";
}

export function SyncHealth() {
  const { t } = useTranslation();
  const health = useQuery(syncHealthQueryOptions());

  return (
    <div className="space-y-6">
      <ErrorBoundary>
        <ItemPanel query={health}>
          {(data) => (
            <div className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <Tile
                  icon={Layers}
                  label={t("admin.sync.totalRoutes")}
                  value={asCount(data.total_routes)}
                />
                <Tile
                  icon={ListChecks}
                  label={t("admin.sync.schedulableRoutes")}
                  value={asCount(data.schedulable_routes)}
                />
                <Tile
                  icon={Lock}
                  label={t("admin.sync.blockedRoutes")}
                  value={asCount(data.blocked_routes)}
                  tone={
                    typeof data.blocked_routes === "number" && data.blocked_routes > 0
                      ? "warn"
                      : "ok"
                  }
                />
                <Tile
                  icon={CalendarClock}
                  label={t("admin.sync.compatibilityPin")}
                  value={
                    typeof data.compatibility_pin === "string"
                      ? data.compatibility_pin
                      : "—"
                  }
                />
              </div>
              <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                {t("admin.sync.dMaxLabel")}
                <Badge variant="outline" className="font-mono tabular-nums">
                  {typeof data.d_max === "string" ? data.d_max : "—"}
                </Badge>
                {/* Which bound the server would validate a pin advance
                    against: a recorded D_max from the last ingest, or the
                    rollover-today fallback when no ingest has run. */}
                {typeof data.d_max_source === "string" && (
                  <Badge variant="secondary">
                    {data.d_max_source === "recorded"
                      ? t("admin.sync.dMaxRecorded")
                      : t("admin.sync.dMaxFallback")}
                  </Badge>
                )}
              </p>
            </div>
          )}
        </ItemPanel>
      </ErrorBoundary>

      <ErrorBoundary>
        <AutoCollectionTab
          path={syncSubscriptionsPath}
          init={{}}
          queryKey={["admin", "sync", "subscriptions"]}
          title={t("admin.sync.subscriptionsHeading")}
          rowIdKey="route_id"
          exclude={["spec_fragment", "identifier_types"]}
        />
      </ErrorBoundary>
    </div>
  );
}

function Tile({
  icon: Icon,
  label,
  value,
  tone = "neutral",
}: {
  icon: typeof Layers;
  label: string;
  value: string;
  tone?: "ok" | "warn" | "neutral";
}) {
  return (
    <div className="space-y-1 rounded-lg border border-border bg-card p-4">
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Icon className="size-3.5 shrink-0" aria-hidden="true" />
        {label}
      </p>
      <p
        className={
          tone === "warn"
            ? "font-mono text-2xl tabular-nums text-amber-500"
            : tone === "ok"
              ? "font-mono text-2xl tabular-nums text-emerald-500"
              : "font-mono text-2xl tabular-nums"
        }
      >
        {value}
      </p>
    </div>
  );
}
