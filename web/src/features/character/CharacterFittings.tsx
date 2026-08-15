import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { characterFittingsPath } from "@/features/character/queries";

// PHASE 20.7 (B48). Capability #8's three /api/v1 endpoints have existed
// since Phase 15 and the sync handler that fills them was written this
// phase — but the SPA had no fittings screen at all, so the capability was
// invisible in the app even once the rows landed. `fittings` appeared in
// web/src/api/schema.d.ts (generated from the OpenAPI document) and in no
// component and no route, which is why the character page showed no tab.
//
// character_id is excluded from the rendered columns for the same reason
// every other character tab excludes it: it is the thing you navigated by,
// so repeating it in every row is noise.
export function CharacterFittings({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  return (
    <AutoCollectionTab
      path={characterFittingsPath}
      init={{ params: { path: { id: characterId } } }}
      queryKey={["characters", characterId, "fittings"]}
      title={t("characters.fittings.heading")}
      rowIdKey="fitting_id"
      exclude={["character_id"]}
    />
  );
}
