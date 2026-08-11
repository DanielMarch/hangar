// Roadmap edge case: "100k-row wallet virtualisation at 60fps requires
// fixed row heights." DataTable virtualizes whatever row count it's given;
// this screen supplies that count by accumulating every fetched page via
// useInfiniteCollection (25 rows/page from the API, up to 100k+ rows
// accumulated client-side as the member pages through their history) —
// cursors are opaque and only ever round-tripped, never parsed.
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { useInfiniteCollection } from "@/api/queries/collection";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/data-table/DataTable";
import {
  dateColumn,
  iskColumn,
  textColumn,
} from "@/components/data-table/columns";
import { Forbidden, isForbidden } from "@/components/PermissionGate";
import { SyncBadge, UnavailablePanel } from "@/components/SyncBadge";
import { TableSkeleton } from "@/components/Skeleton";
import {
  characterWalletJournalPath,
  characterWalletTransactionsPath,
} from "@/features/character/queries";

export function CharacterWallet({ characterId }: { characterId: number }) {
  const { t } = useTranslation();

  const journalColumns = useMemo(
    () => [
      dateColumn("date", t("columns.date")),
      textColumn("ref_type", t("columns.type")),
      iskColumn("amount", t("columns.amount")),
      iskColumn("balance", t("columns.balance")),
      textColumn("description", t("columns.description")),
    ],
    [t],
  );
  const transactionColumns = useMemo(
    () => [
      dateColumn("date", t("columns.date")),
      textColumn("type_id", t("columns.typeId")),
      textColumn("quantity", t("columns.quantity")),
      iskColumn("unit_price", t("columns.unitPrice")),
      textColumn("is_buy", t("columns.buy")),
    ],
    [t],
  );

  const journal = useInfiniteCollection(
    characterWalletJournalPath,
    { params: { path: { id: characterId }, query: { limit: 100 } } },
    ["characters", characterId, "wallet", "journal"],
  );
  const transactions = useInfiniteCollection(
    characterWalletTransactionsPath,
    { params: { path: { id: characterId }, query: { limit: 100 } } },
    ["characters", characterId, "wallet", "transactions"],
  );

  return (
    <div className="space-y-6">
      <InfinitePanel
        title={t("characters.wallet.journal")}
        columns={journalColumns}
        result={journal}
        rowId="journal_id"
      />
      <InfinitePanel
        title={t("characters.wallet.transactions")}
        columns={transactionColumns}
        result={transactions}
        rowId="transaction_id"
      />
    </div>
  );
}

function InfinitePanel({
  title,
  columns,
  result,
  rowId,
}: {
  title: string;
  columns: ReturnType<typeof dateColumn>[];
  result: ReturnType<typeof useInfiniteCollection>;
  rowId: string;
}) {
  const { t } = useTranslation();

  if (result.isPending) return <TableSkeleton />;
  if (result.error) {
    if (isForbidden(result.error))
      return <Forbidden detail={result.error.detail} />;
    throw result.error;
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium text-muted-foreground">{title}</h3>
        {result.sync && <SyncBadge sync={result.sync} />}
      </div>
      {result.rows === null ? (
        <UnavailablePanel reason={result.sync?.blocked_by_pin} />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={result.rows}
            getRowId={(r) => String(r[rowId])}
          />
          {result.hasNextPage && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => result.fetchNextPage()}
              disabled={result.isFetchingNextPage}
            >
              {t("actions.loadMore")}
            </Button>
          )}
        </>
      )}
    </div>
  );
}
