import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { corporationSkyhooksPath } from "@/features/corporation/queries";

export function CorporationSkyhooks({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={corporationSkyhooksPath}
      init={{ params: { path: { id: corporationId } } }}
      queryKey={["corporations", corporationId, "structures", "skyhooks"]}
      title={t("corporations.skyhooks.heading")}
      rowIdKey="skyhook_id"
      exclude={["owner_kind", "owner_id", "reagents"]}
    />
  );
}
