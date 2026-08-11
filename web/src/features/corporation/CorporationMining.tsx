import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import {
  corporationMiningExtractionsPath,
  corporationMiningObserversPath,
} from "@/features/corporation/queries";

export function CorporationMining({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-6">
      <AutoCollectionTab
        path={corporationMiningExtractionsPath}
        init={{ params: { path: { id: corporationId } } }}
        queryKey={["corporations", corporationId, "mining", "extractions"]}
        title={t("corporations.mining.extractions")}
        rowIdKey="moon_id"
        exclude={["corporation_id"]}
      />
      <AutoCollectionTab
        path={corporationMiningObserversPath}
        init={{ params: { path: { id: corporationId } } }}
        queryKey={["corporations", corporationId, "mining", "observers"]}
        title={t("corporations.mining.observers")}
        rowIdKey="observer_id"
        exclude={["corporation_id"]}
      />
    </div>
  );
}
