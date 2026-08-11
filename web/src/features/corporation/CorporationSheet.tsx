import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { ItemPanel } from "@/components/ItemPanel";
import { corporationQueryOptions } from "@/features/corporation/queries";

export function CorporationSheet({ corporationId }: { corporationId: number }) {
  const { t } = useTranslation();
  const query = useQuery(corporationQueryOptions(corporationId));

  return (
    <ItemPanel query={query}>
      {(c) => {
        const rows: Array<[string, React.ReactNode]> = [
          [t("columns.typeId"), typeof c.ticker === "string" ? c.ticker : "—"],
          [t("characters.sheet.corporation"), String(c.alliance_id ?? "—")],
          [t("corporations.tabs.members"), String(c.member_count ?? "—")],
        ];
        return (
          <div className="space-y-4">
            <h2 className="font-mono text-lg font-semibold">
              {String(c.name ?? corporationId)}
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
