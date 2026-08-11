// Roadmap design note (this file's handler-side counterpart,
// internal/api/v1/corporations.go): structures/starbases are exactly the
// resources called out for the blocked_by_pin contract — "required by the
// fuel-low alert" — so AutoCollectionTab -> CollectionTable rendering
// UnavailablePanel for a null `data` here is not a hypothetical edge case,
// it is the documented normal path when the backing ESI route is pinned.
import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { corporationStructuresPath } from "@/features/corporation/queries";

export function CorporationStructures({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={corporationStructuresPath}
      init={{ params: { path: { id: corporationId } } }}
      queryKey={["corporations", corporationId, "structures"]}
      title={t("corporations.structures.heading")}
      rowIdKey="structure_id"
      exclude={["owner_kind", "owner_id", "services"]}
    />
  );
}
