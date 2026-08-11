import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import {
  corporationLedgerBountiesPath,
  corporationLedgerMiningPath,
  corporationLedgerPiPath,
} from "@/features/corporation/queries";

export function CorporationLedgers({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-6">
      <AutoCollectionTab
        path={corporationLedgerBountiesPath}
        init={{ params: { path: { id: corporationId } } }}
        queryKey={["corporations", corporationId, "ledger", "bounties"]}
        title={t("corporations.ledgers.bounties")}
        rowIdKey="character_id"
        exclude={["owner_kind", "owner_id"]}
      />
      <AutoCollectionTab
        path={corporationLedgerMiningPath}
        init={{ params: { path: { id: corporationId } } }}
        queryKey={["corporations", corporationId, "ledger", "mining"]}
        title={t("corporations.ledgers.mining")}
        rowIdKey="character_id"
        exclude={["owner_kind", "owner_id"]}
      />
      <AutoCollectionTab
        path={corporationLedgerPiPath}
        init={{ params: { path: { id: corporationId } } }}
        queryKey={["corporations", corporationId, "ledger", "pi"]}
        title={t("corporations.ledgers.pi")}
        rowIdKey="character_id"
        exclude={["owner_kind", "owner_id"]}
      />
    </div>
  );
}
