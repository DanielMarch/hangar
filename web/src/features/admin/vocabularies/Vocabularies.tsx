// The open-vocabulary board.
//
// ── PHASE 23 (N-4): WHY IT DID NOT EXIST ─────────────────────────────────
//
// Principle 14: every external value HANGAR does not recognise is RECORDED
// rather than rejected. app.open_vocabulary is where they land — cache
// modes and required roles from the ESI spec ingest, notification types
// from the sync handlers — and Gate 6 §6.1's entire demonstration is that a
// spec carrying values nobody anticipated is absorbed instead of refused.
//
// Nothing read the table. Three generated queries had no production caller,
// so an open vocabulary was written and never looked at, which is a
// decision to ignore the thing you deliberately went to the trouble of not
// rejecting. The unknown-SCOPE board next door has existed since Phase 18
// and this is its missing sibling.
//
// The acknowledge action is what keeps a board finite. Without it the list
// grows without bound and gets ignored, which is the same as not having it
// — the reasoning UnknownScopes.tsx records, applied to six more
// vocabularies.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";

import type { Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, numberColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  useAcknowledgeVocabularyValue,
  useVocabularyBoard,
  vocabularyCountsQueryOptions,
} from "@/features/admin/queries";

/**
 * domain.OpenVocabularies(), in the same order.
 *
 * The CATEGORIES are closed — adding one is a code change, as
 * internal/domain/vocabulary.go's type comment says — while the VALUES
 * inside each never are. That asymmetry is what makes listing the
 * categories here legitimate and listing the values a Principle 14
 * violation.
 */
const VOCABULARIES = [
  "ref_type",
  "location_type",
  "notification_type",
  "scope",
  "cache_mode",
  "contract_status",
  "required_role",
] as const;

export function Vocabularies() {
  const { t } = useTranslation();
  const counts = useQuery(vocabularyCountsQueryOptions());
  const [selected, setSelected] = useState<string>(VOCABULARIES[0]);

  const pending = (
    counts.data?.data as { pending?: Record<string, number> } | undefined
  )?.pending;

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {t("admin.vocabularies.intro")}
      </p>
      <nav className="flex flex-wrap gap-1" data-testid="vocabulary-tabs">
        {VOCABULARIES.map((vocabulary) => {
          const n = pending?.[vocabulary] ?? 0;
          return (
            <button
              key={vocabulary}
              type="button"
              onClick={() => setSelected(vocabulary)}
              className={cn(
                "rounded-md px-2.5 py-1.5 font-mono text-xs text-muted-foreground hover:bg-accent hover:text-foreground",
                selected === vocabulary && "bg-accent text-cyan-400",
              )}
            >
              {vocabulary}
              {/* Zero is rendered as nothing rather than as "0". A badge
                  that is always there stops being a signal. */}
              {n > 0 && (
                <span className="ml-1 rounded bg-amber-900/60 px-1 text-amber-200">
                  {n}
                </span>
              )}
            </button>
          );
        })}
      </nav>
      <ErrorBoundary>
        <VocabularyBoard vocabulary={selected} />
      </ErrorBoundary>
    </div>
  );
}

function VocabularyBoard({ vocabulary }: { vocabulary: string }) {
  const { t } = useTranslation();
  const query = useVocabularyBoard(vocabulary);
  const acknowledge = useAcknowledgeVocabularyValue(vocabulary);

  const columns: ColumnDef<Row>[] = [
    {
      id: "value",
      accessorKey: "value",
      header: t("admin.vocabularies.value"),
      meta: { className: "font-mono" },
      cell: ({ getValue }) => String(getValue() ?? "—"),
    },
    numberColumn("occurrences", t("admin.alerts.occurrences")),
    dateColumn("first_seen_at", t("admin.alerts.firstSeen")),
    dateColumn("last_seen_at", t("admin.alerts.lastSeen")),
    {
      id: "acknowledge",
      header: t("admin.scopes.action"),
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="outline"
          data-testid="acknowledge-vocabulary-value"
          disabled={acknowledge.isPending}
          onClick={(e) => {
            e.stopPropagation();
            acknowledge.mutate(String(row.original.value));
          }}
        >
          {t("admin.acknowledge")}
        </Button>
      ),
    },
  ];

  return (
    <CollectionTable
      query={query}
      columns={columns}
      title={t("admin.vocabularies.heading")}
      getRowId={(r) => `${vocabulary}:${String(r.value)}`}
    />
  );
}
