// Roadmap edge case: "Contract items drawer: a courier contract
// legitimately has no items; render 'no items' rather than a loading state
// that never resolves." The items/bids queries below are ordinary
// useCollection calls — an empty `[]` resolves normally and
// ContractDrawer renders the explicit "no items"/"no bids" copy for it, as
// distinct from the still-loading and blocked_by_pin states.
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import {
  dateColumn,
  iskColumn,
  textColumn,
} from "@/components/data-table/columns";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Forbidden, isForbidden } from "@/components/PermissionGate";
import { TableSkeleton } from "@/components/Skeleton";
import { UnavailablePanel } from "@/components/SyncBadge";
import type { Row } from "@/api/queries/collection";
import {
  characterContractBidsPath,
  characterContractItemsPath,
  characterContractsPath,
} from "@/features/character/queries";

export function CharacterContracts({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  const contractColumns = useMemo(
    () => [
      textColumn("contract_id", t("columns.itemId")),
      textColumn("type", t("columns.type")),
      textColumn("status", t("columns.status")),
      iskColumn("price", t("columns.price")),
      iskColumn("reward", t("columns.reward")),
      iskColumn("collateral", t("columns.collateral")),
      dateColumn("date_expired", t("columns.expires")),
    ],
    [t],
  );
  const contracts = useCollection(
    characterContractsPath,
    { params: { path: { id: characterId } } },
    ["characters", characterId, "contracts"],
  );
  const [selected, setSelected] = useState<Row | null>(null);

  return (
    <div className="space-y-4">
      <CollectionTable
        query={contracts}
        columns={contractColumns}
        title={t("characters.contracts.heading")}
        getRowId={(r) => String(r.contract_id)}
        onRowClick={(row) => setSelected(row)}
      />
      <Dialog
        open={selected !== null}
        onOpenChange={(open) => !open && setSelected(null)}
      >
        <DialogContent className="max-w-2xl">
          {selected && (
            <ContractDrawer characterId={characterId} contract={selected} />
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function ContractDrawer({
  characterId,
  contract,
}: {
  characterId: number;
  contract: Row;
}) {
  const { t } = useTranslation();
  const contractId = Number(contract.contract_id);

  const items = useCollection(
    characterContractItemsPath,
    { params: { path: { id: characterId, sub_id: contractId } } },
    ["characters", characterId, "contracts", contractId, "items"],
  );
  const bids = useCollection(
    characterContractBidsPath,
    { params: { path: { id: characterId, sub_id: contractId } } },
    ["characters", characterId, "contracts", contractId, "bids"],
  );

  return (
    <div className="space-y-4">
      <DialogHeader>
        <DialogTitle className="font-mono">
          {t("characters.contracts.heading")} #{contractId}
        </DialogTitle>
      </DialogHeader>

      <Section
        title={t("characters.contracts.items")}
        query={items}
        emptyMessage={t("characters.contracts.noItems")}
      >
        {(rows) => (
          <ul className="space-y-1 text-sm">
            {rows.map((r) => (
              <li
                key={String(r.record_id)}
                className="flex items-center justify-between"
              >
                <span className="font-mono tabular-nums">{`${t("columns.typeId")} ${String(r.type_id)}`}</span>
                <span className="font-mono tabular-nums">
                  ×{String(r.quantity)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section
        title={t("characters.contracts.bids")}
        query={bids}
        emptyMessage={t("characters.contracts.noBids")}
      >
        {(rows) => (
          <ul className="space-y-1 text-sm">
            {rows.map((r) => (
              <li
                key={String(r.bid_id)}
                className="flex items-center justify-between"
              >
                <span className="font-mono tabular-nums">
                  {String(r.bidder_id)}
                </span>
                <span className="font-mono tabular-nums">
                  {String(r.amount)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}

function Section({
  title,
  query,
  emptyMessage,
  children,
}: {
  title: string;
  query: ReturnType<typeof useCollection>;
  emptyMessage: string;
  children: (rows: Row[]) => React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <h4 className="text-xs font-medium text-muted-foreground uppercase">
        {title}
      </h4>
      {query.isPending ? (
        <TableSkeleton rows={2} />
      ) : query.error ? (
        isForbidden(query.error) ? (
          <Forbidden detail={query.error.detail} />
        ) : (
          (() => {
            throw query.error;
          })()
        )
      ) : query.data?.rows === null ? (
        <UnavailablePanel reason={query.data?.sync?.blocked_by_pin} />
      ) : (query.data?.rows.length ?? 0) === 0 ? (
        <p className="text-sm text-muted-foreground">{emptyMessage}</p>
      ) : (
        children(query.data!.rows as Row[])
      )}
    </div>
  );
}
