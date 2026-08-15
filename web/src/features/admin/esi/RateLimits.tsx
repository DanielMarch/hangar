// Governor 1's ledger dashboard, and Governor 2's error budget beside it.
//
// LEDGER DIVERGENCE IS THE HEADLINE. `divergence` is the same quantity
// Gate 1.3 measures (`max(esi_ledger_divergence) over the run <= 1, per
// group`). Sustained divergence is the early warning that the
// cluster-shared ledger and ESI's own X-Ratelimit-Remaining have drifted
// apart, which is a Gate 1 failure in the making, so it leads the screen
// rather than sitting in a column the operator has to scroll to.
//
// ── PHASE 20.4: WHICH TWO COLUMNS THE DIVERGENCE IS THE DIFFERENCE OF ────
// It is |local_at_reading - server_remaining|, NOT |local_remaining -
// server_remaining|, and the table shows all three so the arithmetic is
// legible rather than something the operator has to take on trust.
//
//   local_remaining    what the ledger holds RIGHT NOW, summed live
//   local_at_reading   what it held at the instant the server reading
//                      below was recorded, written in the same statement
//   server_remaining   the last X-Ratelimit-Remaining CCP sent
//
// The first and third describe different moments; subtracting them
// measures how much has been consumed since the last reconcile, which read
// 40-55 on healthy buckets on the live installation against a tolerance of
// 1. The second and third describe one moment, which is what makes their
// difference a measurement at all. `local_remaining` stays on the screen
// because current headroom is a real operator question — it is just not
// this one.
//
// A null divergence means the server has said nothing about that bucket
// yet — rendered as "not observed", never as zero. Zero divergence is a
// healthy reading; no reading is not a reading, and collapsing them would
// hide a bucket whose headers have stopped arriving behind a wall of
// reassuring zeroes.
import { AlertTriangle, Activity } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";
import { useQuery } from "@tanstack/react-query";

import { useCollection, type Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, numberColumn, textColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { ItemPanel } from "@/components/ItemPanel";
import { Badge } from "@/components/ui/badge";
import { errorLimitQueryOptions, rateLimitsPath } from "@/features/admin/queries";

/** Gate 1.3's own threshold: divergence above this is out of tolerance. */
const DIVERGENCE_TOLERANCE = 1;

function divergenceOf(row: Row): number | null {
  const v = row.divergence;
  return typeof v === "number" ? v : null;
}

export function RateLimits() {
  const { t } = useTranslation();
  const query = useCollection(rateLimitsPath, {}, ["admin", "esi", "ratelimits"]);
  const rows = query.data?.rows ?? [];

  const observed = rows
    .map(divergenceOf)
    .filter((d): d is number => d !== null);
  const worst = observed.length > 0 ? Math.max(...observed) : null;
  const unobserved = rows.length - observed.length;
  const breaching = observed.filter((d) => d > DIVERGENCE_TOLERANCE).length;

  const columns: ColumnDef<Row>[] = [
    textColumn("rate_limit_group", t("admin.rateLimits.group")),
    textColumn("user_key", t("admin.rateLimits.userKey")),
    numberColumn("max_tokens", t("admin.rateLimits.maxTokens")),
    numberColumn("local_remaining", t("admin.rateLimits.localRemaining")),
    numberColumn("local_remaining_at_reading", t("admin.rateLimits.localAtReading")),
    numberColumn("server_remaining", t("admin.rateLimits.serverRemaining")),
    {
      id: "divergence",
      accessorKey: "divergence",
      header: t("admin.rateLimits.divergence"),
      meta: { className: "font-mono tabular-nums text-right" },
      cell: ({ row }) => <DivergenceCell row={row.original} />,
    },
    dateColumn("server_observed_at", t("admin.rateLimits.serverObservedAt")),
  ];

  return (
    <div className="space-y-6">
      <div className="grid gap-3 sm:grid-cols-3">
        <StatCard
          label={t("admin.rateLimits.worstDivergence")}
          value={worst === null ? t("admin.rateLimits.notObserved") : String(worst)}
          tone={worst !== null && worst > DIVERGENCE_TOLERANCE ? "bad" : "ok"}
          icon={worst !== null && worst > DIVERGENCE_TOLERANCE ? AlertTriangle : Activity}
          hint={t("admin.rateLimits.gateHint", { tolerance: DIVERGENCE_TOLERANCE })}
        />
        <StatCard
          label={t("admin.rateLimits.bucketsBreaching")}
          value={String(breaching)}
          tone={breaching > 0 ? "bad" : "ok"}
          icon={AlertTriangle}
          hint={t("admin.rateLimits.bucketsBreachingHint")}
        />
        <StatCard
          label={t("admin.rateLimits.bucketsUnobserved")}
          value={String(unobserved)}
          tone="neutral"
          icon={Activity}
          hint={t("admin.rateLimits.bucketsUnobservedHint")}
        />
      </div>

      <ErrorBoundary>
        <ErrorBudget />
      </ErrorBoundary>

      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.rateLimits.heading")}
        getRowId={(r, i) => `${String(r.rate_limit_group)}:${String(r.user_key)}:${i}`}
      />
    </div>
  );
}

function DivergenceCell({ row }: { row: Row }) {
  const { t } = useTranslation();
  const d = divergenceOf(row);
  if (d === null) {
    return (
      <span className="text-muted-foreground">
        {t("admin.rateLimits.notObserved")}
      </span>
    );
  }
  if (d > DIVERGENCE_TOLERANCE) {
    return <Badge variant="destructive">{d}</Badge>;
  }
  return <span>{d}</span>;
}

function StatCard({
  label,
  value,
  hint,
  tone,
  icon: Icon,
}: {
  label: string;
  value: string;
  hint: string;
  tone: "ok" | "bad" | "neutral";
  icon: typeof Activity;
}) {
  return (
    <div className="space-y-1 rounded-lg border border-border bg-card p-4">
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Icon className="size-3.5 shrink-0" aria-hidden="true" />
        {label}
      </p>
      <p
        className={
          tone === "bad"
            ? "font-mono text-2xl tabular-nums text-destructive"
            : tone === "ok"
              ? "font-mono text-2xl tabular-nums text-emerald-500"
              : "font-mono text-2xl tabular-nums"
        }
      >
        {value}
      </p>
      <p className="text-xs text-muted-foreground">{hint}</p>
    </div>
  );
}

/** Governor 2 — the installation-wide ESI error budget. */
function ErrorBudget() {
  const { t } = useTranslation();
  const query = useQuery(errorLimitQueryOptions());
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium text-muted-foreground">
        {t("admin.errorLimit.heading")}
      </h3>
      <ItemPanel query={query}>
        {(data) => (
          <div className="flex flex-wrap items-center gap-4 rounded-lg border border-border bg-card p-4">
            <Field label={t("admin.errorLimit.errorCount")} value={String(data.error_count ?? "—")} />
            <Field
              label={t("admin.errorLimit.windowStart")}
              value={
                typeof data.window_start === "string"
                  ? new Date(data.window_start).toLocaleString()
                  : "—"
              }
            />
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">
                {t("admin.errorLimit.paused")}
              </p>
              {data.paused ? (
                <Badge variant="destructive">{t("admin.errorLimit.pausedYes")}</Badge>
              ) : (
                <Badge variant="secondary">{t("admin.errorLimit.pausedNo")}</Badge>
              )}
            </div>
          </div>
        )}
      </ItemPanel>
    </section>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="space-y-0.5">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="font-mono text-sm tabular-nums">{value}</p>
    </div>
  );
}
