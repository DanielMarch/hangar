import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import {
  dateColumn,
  numberColumn,
  textColumn,
} from "@/components/data-table/columns";
import {
  characterSkillqueuePath,
  characterSkillsPath,
} from "@/features/character/queries";

export function CharacterSkills({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  const skills = useCollection(
    characterSkillsPath,
    { params: { path: { id: characterId } } },
    ["characters", characterId, "skills"],
  );
  const queue = useCollection(
    characterSkillqueuePath,
    { params: { path: { id: characterId } } },
    ["characters", characterId, "skillqueue"],
  );

  const skillColumns = useMemo(
    () => [
      textColumn("skill_id", t("columns.skill")),
      numberColumn("trained_level", t("columns.level")),
      numberColumn("skillpoints", t("columns.sp")),
    ],
    [t],
  );
  const queueColumns = useMemo(
    () => [
      numberColumn("queue_position", t("columns.queuePosition")),
      textColumn("skill_id", t("columns.skill")),
      numberColumn("finished_level", t("columns.level")),
      dateColumn("start_date", t("columns.start")),
      dateColumn("finish_date", t("columns.finish")),
    ],
    [t],
  );

  const totalSp = (skills.data?.rows ?? []).reduce(
    (sum, row) =>
      sum + (typeof row.skillpoints === "number" ? row.skillpoints : 0),
    0,
  );

  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground">
        {t("characters.skills.totalSp")}:{" "}
        <span className="font-mono tabular-nums text-foreground">
          {totalSp.toLocaleString()}
        </span>
      </p>
      <CollectionTable
        query={queue}
        columns={queueColumns}
        title={t("characters.skills.queue")}
        getRowId={(r) => String(r.queue_position)}
      />
      <CollectionTable
        query={skills}
        columns={skillColumns}
        title={t("characters.skills.trained")}
        getRowId={(r) => String(r.skill_id)}
      />
    </div>
  );
}
