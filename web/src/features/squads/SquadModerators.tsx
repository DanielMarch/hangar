import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { squadModeratorsPath } from "@/features/squads/queries";

export function SquadModerators({ squadId }: { squadId: string }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={squadModeratorsPath}
      init={{ params: { path: { id: squadId } } }}
      queryKey={["squads", squadId, "moderators"]}
      title={t("squads.tabs.moderators")}
      rowIdKey="user_id"
    />
  );
}
