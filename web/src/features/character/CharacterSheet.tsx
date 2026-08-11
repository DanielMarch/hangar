import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { ItemPanel } from "@/components/ItemPanel";
import { characterQueryOptions } from "@/features/character/queries";

export function CharacterSheet({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  const query = useQuery(characterQueryOptions(characterId));

  return (
    <ItemPanel query={query}>
      {(c) => {
        const rows: Array<[string, React.ReactNode]> = [
          [t("characters.sheet.corporation"), String(c.corporation_id ?? "—")],
          [
            t("characters.sheet.security"),
            typeof c.security_status === "number"
              ? c.security_status.toFixed(2)
              : "—",
          ],
          [
            t("characters.sheet.title"),
            typeof c.title === "string" ? c.title : "—",
          ],
          [
            t("characters.sheet.birthday"),
            typeof c.birthday === "string"
              ? new Date(c.birthday).toLocaleDateString()
              : "—",
          ],
        ];
        return (
          <div className="space-y-4">
            <h2 className="font-mono text-lg font-semibold">
              {String(c.name ?? characterId)}
            </h2>
            <dl className="grid max-w-md grid-cols-2 gap-x-4 gap-y-2 text-sm">
              {rows.map(([label, value]) => (
                <div key={label} className="contents">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="font-mono tabular-nums">{value}</dd>
                </div>
              ))}
            </dl>
          </div>
        );
      }}
    </ItemPanel>
  );
}
