import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { characterCalendarPath } from "@/features/character/queries";

export function CharacterCalendar({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={characterCalendarPath}
      init={{ params: { path: { id: characterId } } }}
      queryKey={["characters", characterId, "calendar"]}
      title={t("characters.calendar.heading")}
      rowIdKey="event_id"
      exclude={["character_id"]}
    />
  );
}
