import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { characterIntelPath } from "@/features/character/queries";

export function CharacterIntel({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={characterIntelPath}
      init={{ params: { path: { id: characterId } } }}
      queryKey={["characters", characterId, "intel"]}
      title={t("characters.intel.heading")}
      rowIdKey="target_character_id"
    />
  );
}
