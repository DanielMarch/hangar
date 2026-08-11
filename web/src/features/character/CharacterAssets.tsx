// Roadmap edge case: "The asset tree must render to depth 5 in < 2s. Fetch
// the whole subtree in one request (the recursive CTE) rather than one
// request per level." internal/store/asset.go's AssetTree does exactly
// that — GET .../assets/tree/{location_id} returns the whole bounded-depth
// subtree as one flat array of { asset, depth, path }, which this renders
// as an indented list (depth * indent) rather than a real recursive
// component tree — no re-fetching per level, no N+1.
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { Forbidden, isForbidden } from "@/components/PermissionGate";
import { UnavailablePanel } from "@/components/SyncBadge";
import { TableSkeleton } from "@/components/Skeleton";
import {
  characterAssetTreePath,
  characterAssetsPath,
} from "@/features/character/queries";
import {
  idColumn,
  numberColumn,
  textColumn,
} from "@/components/data-table/columns";

export function CharacterAssets({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  const flatColumns = useMemo(
    () => [
      idColumn("item_id", t("columns.itemId")),
      idColumn("type_id", t("columns.typeId")),
      idColumn("location_id", t("columns.locationId")),
      textColumn("location_flag", t("columns.flag")),
      numberColumn("quantity", t("columns.quantity")),
    ],
    [t],
  );
  const flat = useCollection(
    characterAssetsPath,
    { params: { path: { id: characterId }, query: { limit: 100 } } },
    ["characters", characterId, "assets"],
  );

  const candidateRoots = useMemo(() => {
    const rows = flat.data?.rows ?? [];
    const ids = new Set<number>();
    for (const r of rows) {
      if (typeof r.location_id === "number") ids.add(r.location_id);
    }
    return Array.from(ids).slice(0, 25);
  }, [flat.data]);

  const [selectedRoot, setSelectedRoot] = useState<number | undefined>(
    undefined,
  );
  const rootLocationId = selectedRoot ?? candidateRoots[0];

  const tree = useCollection(
    characterAssetTreePath,
    { params: { path: { id: characterId, location_id: rootLocationId ?? 0 } } },
    ["characters", characterId, "assets", "tree", rootLocationId],
    rootLocationId !== undefined,
  );

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium text-muted-foreground">
            {t("characters.assets.location")}
          </h3>
          {candidateRoots.length > 1 && (
            <select
              className="h-8 rounded-md border border-border bg-background px-2 text-sm font-mono"
              value={rootLocationId ?? ""}
              onChange={(e) => setSelectedRoot(Number(e.target.value))}
            >
              {candidateRoots.map((id) => (
                <option key={id} value={id}>
                  {id}
                </option>
              ))}
            </select>
          )}
        </div>
        <AssetTreeView
          characterId={characterId}
          rootLocationId={rootLocationId}
          query={tree}
        />
      </div>

      <CollectionTable
        query={flat}
        columns={flatColumns}
        title={t("characters.tabs.assets")}
        getRowId={(r) => String(r.item_id)}
      />
    </div>
  );
}

function AssetTreeView({
  rootLocationId,
  query,
}: {
  characterId: number;
  rootLocationId?: number;
  query: ReturnType<typeof useCollection>;
}) {
  const { t } = useTranslation();

  if (rootLocationId === undefined) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("dataTable.noResults")}
      </p>
    );
  }
  if (query.isPending) return <TableSkeleton rows={4} />;
  if (query.error) {
    if (isForbidden(query.error))
      return <Forbidden detail={query.error.detail} />;
    throw query.error;
  }

  const rows = query.data?.rows;
  if (rows === null) {
    return <UnavailablePanel reason={query.data?.sync?.blocked_by_pin} />;
  }
  if (!rows || rows.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("dataTable.noResults")}
      </p>
    );
  }

  return (
    <div className="space-y-1 rounded-md border border-border bg-card p-2">
      {rows.map((node, i) => {
        const asset = (node.asset ?? {}) as Record<string, unknown>;
        const depth = typeof node.depth === "number" ? node.depth : 0;
        return (
          <div
            key={i}
            className="flex items-center gap-2 truncate text-sm"
            style={{ paddingLeft: depth * 16 }}
          >
            <span className="font-mono tabular-nums text-muted-foreground">
              {String(asset.item_id ?? "—")}
            </span>
            <span className="truncate">
              {typeof asset.name === "string" && asset.name
                ? asset.name
                : `${t("columns.typeId")} ${String(asset.type_id ?? "?")}`}
            </span>
            <span className="font-mono tabular-nums text-muted-foreground">
              ×{String(asset.quantity ?? 1)}
            </span>
          </div>
        );
      })}
    </div>
  );
}
