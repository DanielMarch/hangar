import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { corporationMembersPath } from "@/features/corporation/queries";

export function CorporationMembers({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={corporationMembersPath}
      init={{ params: { path: { id: corporationId } } }}
      queryKey={["corporations", corporationId, "members"]}
      title={t("corporations.tabs.members")}
      rowIdKey="character_id"
    />
  );
}
