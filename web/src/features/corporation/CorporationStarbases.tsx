import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { corporationStarbasesPath } from "@/features/corporation/queries";

export function CorporationStarbases({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={corporationStarbasesPath}
      init={{ params: { path: { id: corporationId } } }}
      queryKey={["corporations", corporationId, "starbases"]}
      title={t("corporations.starbases.heading")}
      rowIdKey="starbase_id"
      exclude={["owner_kind", "owner_id"]}
    />
  );
}
