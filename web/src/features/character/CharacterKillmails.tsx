import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { characterKillmailsPath } from "@/features/character/queries";

export function CharacterKillmails({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={characterKillmailsPath}
      init={{ params: { path: { id: characterId } } }}
      queryKey={["characters", characterId, "killmails"]}
      title={t("characters.killmails.heading")}
      rowIdKey="killmail_id"
      exclude={["owner_kind", "owner_id"]}
    />
  );
}
