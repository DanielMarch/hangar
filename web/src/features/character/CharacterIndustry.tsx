import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { characterIndustryJobsPath } from "@/features/character/queries";

export function CharacterIndustry({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={characterIndustryJobsPath}
      init={{ params: { path: { id: characterId } } }}
      queryKey={["characters", characterId, "industry", "jobs"]}
      title={t("characters.industry.heading")}
      rowIdKey="job_id"
      exclude={["owner_kind", "owner_id"]}
    />
  );
}
