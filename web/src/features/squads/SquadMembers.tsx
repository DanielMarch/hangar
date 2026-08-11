import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { squadMembersPath } from "@/features/squads/queries";

export function SquadMembers({ squadId }: { squadId: string }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={squadMembersPath}
      init={{ params: { path: { id: squadId } } }}
      queryKey={["squads", squadId, "members"]}
      title={t("squads.tabs.members")}
      rowIdKey="character_id"
      exclude={["squad_id"]}
    />
  );
}
