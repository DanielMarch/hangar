import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { CollectionTable } from "@/components/data-table/CollectionTable";
import { textColumn } from "@/components/data-table/columns";
import { squadsListQueryOptions } from "@/features/squads/queries";

export const Route = createFileRoute("/_authed/squads/")({
  staticData: { breadcrumbKey: "nav.squads" },
  component: SquadsIndex,
});

function SquadsIndex() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const squads = useQuery(squadsListQueryOptions());

  const columns = [
    textColumn("name", t("columns.description")),
    textColumn("type", t("columns.type")),
  ];

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">{t("squads.heading")}</h1>
      <CollectionTable
        query={squads}
        columns={columns}
        getRowId={(r) => String(r.squad_id)}
        onRowClick={(row) =>
          void navigate({
            to: "/squads/$squadId",
            params: { squadId: String(row.squad_id) },
          })
        }
      />
    </div>
  );
}
