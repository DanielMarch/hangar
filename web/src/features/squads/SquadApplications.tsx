// "squads.moderate" gated (stricter than the "squads.view" the other
// tabs use) — a 403 here is expected for a plain squad member browsing
// their own squad and degrades via CollectionTable's Forbidden panel same
// as everywhere else.
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { Button } from "@/components/ui/button";
import {
  squadApplicationsPath,
  useResolveApplication,
} from "@/features/squads/queries";

export function SquadApplications({ squadId }: { squadId: string }) {
  const { t } = useTranslation();
  const applications = useCollection(
    squadApplicationsPath,
    { params: { path: { id: squadId } } },
    ["squads", squadId, "applications"],
  );
  const resolve = useResolveApplication(squadId);

  const columns = [
    {
      id: "character_id",
      accessorKey: "character_id",
      header: t("columns.itemId"),
      meta: { className: "font-mono tabular-nums" },
    },
    { id: "message", accessorKey: "message", header: t("columns.description") },
    {
      id: "actions",
      header: "",
      cell: ({ row }: { row: { original: Record<string, unknown> } }) => (
        <div className="flex gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={resolve.isPending}
            onClick={() =>
              resolve.mutate({
                applicationId: String(row.original.application_id),
                approve: true,
              })
            }
          >
            {t("actions.approve")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={resolve.isPending}
            onClick={() =>
              resolve.mutate({
                applicationId: String(row.original.application_id),
                approve: false,
              })
            }
          >
            {t("actions.reject")}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <CollectionTable
      query={applications}
      columns={columns}
      title={t("squads.applications.pending")}
      getRowId={(r) => String(r.application_id)}
    />
  );
}
