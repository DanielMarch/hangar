import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { textColumn, iskColumn } from "@/components/data-table/columns";
import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Row } from "@/api/queries/collection";
import {
  corporationProjectContributorsPath,
  corporationProjectsPath,
} from "@/features/corporation/queries";

export function CorporationProjects({
  corporationId,
}: {
  corporationId: number;
}) {
  const { t } = useTranslation();
  const projects = useCollection(
    corporationProjectsPath,
    { params: { path: { id: corporationId } } },
    ["corporations", corporationId, "projects"],
  );
  const [selected, setSelected] = useState<Row | null>(null);

  const columns = [
    textColumn("name", t("columns.description")),
    textColumn("state", t("columns.status")),
    iskColumn("reward_isk", t("columns.reward")),
  ];

  return (
    <div className="space-y-4">
      <CollectionTable
        query={projects}
        columns={columns}
        title={t("corporations.tabs.projects")}
        getRowId={(r) => String(r.project_id)}
        onRowClick={(row) => setSelected(row)}
      />
      <Dialog
        open={selected !== null}
        onOpenChange={(open) => !open && setSelected(null)}
      >
        <DialogContent className="max-w-2xl">
          {selected && (
            <div className="space-y-4">
              <DialogHeader>
                <DialogTitle>{String(selected.name ?? "")}</DialogTitle>
              </DialogHeader>
              <AutoCollectionTab
                path={corporationProjectContributorsPath}
                init={{
                  params: {
                    path: {
                      id: corporationId,
                      project_id: String(selected.project_id),
                    },
                  },
                }}
                queryKey={[
                  "corporations",
                  corporationId,
                  "projects",
                  String(selected.project_id),
                  "contributors",
                ]}
                title={t("corporations.projects.contributors")}
                rowIdKey="character_id"
              />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
