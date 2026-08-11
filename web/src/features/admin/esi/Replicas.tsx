// The live replica registry and the ledger mode it implies.
//
// READ-ONLY BY DESIGN. There is deliberately no control here to set the
// ledger mode: the mode is DERIVED from how many replicas are alive
// (SRS v3.1 §4.1.3), never chosen. An operator-settable mode is exactly
// defect B1 — a solo-mode fast path engaged while a second replica is live
// double-spends the shared budget — and adding a "force clustered" toggle
// to this screen would reintroduce it. If the mode shown here is wrong,
// the replica registry is wrong; fix that, not the mode.
import { Network } from "lucide-react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { Badge } from "@/components/ui/badge";
import { replicasPath } from "@/features/admin/queries";

export function Replicas() {
  const { t } = useTranslation();
  const query = useCollection(replicasPath, {}, ["admin", "esi", "replicas"]);
  const live = query.data?.rows?.length ?? 0;

  // The same derivation internal/esi/ratelimit/mode.go makes: one live
  // replica means the solo fast path is safe, more than one means the
  // cluster-shared ledger is mandatory.
  const mode = live <= 1 ? "solo" : "clustered";

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-card p-4">
        <Network className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <div className="space-y-0.5">
          <p className="text-xs text-muted-foreground">
            {t("admin.replicas.modeLabel")}
          </p>
          <Badge variant={mode === "solo" ? "secondary" : "default"}>
            {mode === "solo"
              ? t("admin.replicas.modeSolo")
              : t("admin.replicas.modeClustered")}
          </Badge>
        </div>
        <div className="space-y-0.5">
          <p className="text-xs text-muted-foreground">
            {t("admin.replicas.liveCount")}
          </p>
          <p className="font-mono text-sm tabular-nums">{live}</p>
        </div>
        <p className="max-w-md text-xs text-muted-foreground">
          {t("admin.replicas.derivedNote")}
        </p>
      </div>

      <AutoCollectionTab
        path={replicasPath}
        init={{}}
        queryKey={["admin", "esi", "replicas"]}
        title={t("admin.replicas.heading")}
        rowIdKey="replica_id"
      />
    </div>
  );
}
