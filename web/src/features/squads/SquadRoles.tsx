import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { squadRolesPath } from "@/features/squads/queries";

export function SquadRoles({ squadId }: { squadId: string }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={squadRolesPath}
      init={{ params: { path: { id: squadId } } }}
      queryKey={["squads", squadId, "roles"]}
      title={t("squads.tabs.roles")}
      rowIdKey="role_id"
    />
  );
}
