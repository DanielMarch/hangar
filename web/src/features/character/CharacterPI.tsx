// PI colony summaries only (planet/type/upgrade level/pin count) — NOT a
// pin/link/route diagram. internal/api/dto/row.go's generic Row() hex-
// encodes any []byte-kind field, and Go's `json.RawMessage` IS a []byte
// under the hood, so AppPlanetColonyDetail's `pins`/`links`/`routes`
// (GET .../planets/{sub_id}) arrive on the wire as opaque hex strings, not
// the structured JSON a colony diagram needs. That's a backend response-
// shaping gap pre-existing this phase, not something this screen can work
// around by itself — flagged in the Phase 17 report rather than silently
// worked around with client-side hex/JSON double-decoding of what should
// be a typed response.
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import {
  idColumn,
  numberColumn,
  textColumn,
} from "@/components/data-table/columns";
import { characterPlanetsPath } from "@/features/character/queries";

export function CharacterPI({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  const colonies = useCollection(
    characterPlanetsPath,
    { params: { path: { id: characterId } } },
    ["characters", characterId, "planets"],
  );

  const columns = useMemo(
    () => [
      idColumn("planet_id", t("columns.itemId")),
      textColumn("planet_type", t("columns.type")),
      idColumn("solar_system_id", t("columns.locationId")),
      numberColumn("upgrade_level", t("columns.level")),
      numberColumn("num_pins", t("characters.pi.colonies")),
    ],
    [t],
  );

  return (
    <CollectionTable
      query={colonies}
      columns={columns}
      title={t("characters.pi.colonies")}
      getRowId={(r) => String(r.planet_id)}
    />
  );
}
