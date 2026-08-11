import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useCollection, useInfiniteCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import {
  dateColumn,
  iskColumn,
  numberColumn,
  textColumn,
} from "@/components/data-table/columns";
import { DataTable } from "@/components/data-table/DataTable";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Forbidden, isForbidden } from "@/components/PermissionGate";
import { SyncBadge, UnavailablePanel } from "@/components/SyncBadge";
import { TableSkeleton } from "@/components/Skeleton";
import {
  corporationWalletJournalPath,
  corporationWalletTransactionsPath,
  corporationWalletsPath,
} from "@/features/corporation/queries";

export function CorporationWallets({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  const wallets = useCollection(
    corporationWalletsPath,
    { params: { path: { id: corporationId } } },
    ["corporations", corporationId, "wallets"],
  );
  const [division, setDivision] = useState<number | null>(null);

  const columns = useMemo(
    () => [
      numberColumn("division", t("corporations.wallets.division")),
      iskColumn("balance", t("columns.balance")),
    ],
    [t],
  );

  return (
    <div className="space-y-4">
      <CollectionTable
        query={wallets}
        columns={columns}
        title={t("corporations.tabs.wallets")}
        getRowId={(r) => String(r.division)}
        onRowClick={(row) => setDivision(Number(row.division))}
      />
      <Dialog
        open={division !== null}
        onOpenChange={(open) => !open && setDivision(null)}
      >
        <DialogContent className="max-w-3xl">
          {division !== null && (
            <WalletDivisionDrawer
              corporationId={corporationId}
              division={division}
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function WalletDivisionDrawer({
  corporationId,
  division,
}: {
  corporationId: number;
  division: number;
}) {
  const { t } = useTranslation();

  const journalColumns = useMemo(
    () => [
      dateColumn("date", t("columns.date")),
      textColumn("ref_type", t("columns.type")),
      iskColumn("amount", t("columns.amount")),
      iskColumn("balance", t("columns.balance")),
    ],
    [t],
  );
  const transactionColumns = useMemo(
    () => [
      dateColumn("date", t("columns.date")),
      textColumn("type_id", t("columns.typeId")),
      iskColumn("unit_price", t("columns.unitPrice")),
    ],
    [t],
  );

  const journal = useInfiniteCollection(
    corporationWalletJournalPath,
    {
      params: { path: { id: corporationId, division }, query: { limit: 100 } },
    },
    ["corporations", corporationId, "wallets", division, "journal"],
  );
  const transactions = useInfiniteCollection(
    corporationWalletTransactionsPath,
    {
      params: { path: { id: corporationId, division }, query: { limit: 100 } },
    },
    ["corporations", corporationId, "wallets", division, "transactions"],
  );

  return (
    <div className="space-y-4">
      <DialogHeader>
        <DialogTitle className="font-mono">
          {t("corporations.wallets.division")} {division}
        </DialogTitle>
      </DialogHeader>
      <InfinitePanel
        title={t("corporations.wallets.journal")}
        columns={journalColumns}
        result={journal}
        rowId="journal_id"
      />
      <InfinitePanel
        title={t("corporations.wallets.transactions")}
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

  if (result.isPending) return <TableSkeleton rows={3} />;
  if (result.error) {
    if (isForbidden(result.error))
      return <Forbidden detail={result.error.detail} />;
    throw result.error;
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h4 className="text-xs font-medium text-muted-foreground uppercase">
          {title}
        </h4>
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
            maxHeightClassName="max-h-64"
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
