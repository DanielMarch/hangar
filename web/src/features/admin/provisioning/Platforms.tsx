// Access-provisioning platforms: the list, and one platform's detail
// (lockdown, rule editor, exposure board).
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ColumnDef } from "@tanstack/react-table";
import { useQuery } from "@tanstack/react-query";

import type { Row } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { boolColumn, textColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ExposureBoard } from "@/features/admin/provisioning/ExposureBoard";
import { RuleEditor } from "@/features/admin/provisioning/RuleEditor";
import { Input } from "@/components/ui/input";
import {
  platformsQueryOptions,
  useCreatePlatform,
  useCreatePlatformGroup,
  useLockdown,
  usePlatformGroups,
} from "@/features/admin/queries";

/** app.platform.kind's CHECK constraint — domain.PlatformKinds(). */
const PLATFORM_KINDS = ["discord", "teamspeak", "mumble"] as const;

export function Platforms() {
  const { t } = useTranslation();
  const query = useQuery(platformsQueryOptions());
  const [selected, setSelected] = useState<string | null>(null);

  const columns: ColumnDef<Row>[] = [
    textColumn("name", t("admin.platforms.name")),
    textColumn("kind", t("admin.platforms.kind")),
    boolColumn("enabled", t("admin.platforms.enabled")),
    {
      id: "locked_down",
      accessorKey: "locked_down",
      header: t("admin.platforms.lockdown"),
      cell: ({ getValue }) =>
        getValue() ? (
          <Badge variant="destructive">{t("admin.platforms.lockedDown")}</Badge>
        ) : (
          <Badge variant="secondary">{t("admin.platforms.live")}</Badge>
        ),
    },
  ];

  const platforms = query.data?.rows ?? [];
  const current = platforms.find((p) => String(p.platform_id) === selected);

  return (
    <div className="space-y-6">
      {/* ── PHASE 23 (N-4) ───────────────────────────────────────────────
          app.platform had NO production writer at all until this phase, so
          no installation could create a platform row by any means the
          product offered — cmd/hangar/discord.go's own comment says as much
          before warning and registering no driver. Phases 11-13 built three
          provisioning drivers, an entitlement engine, an exposure board and
          the revocation SLO Gate 2 measures, on top of a table nothing
          could populate. */}
      <NewPlatform />
      <CollectionTable
        query={query}
        columns={columns}
        title={t("admin.platforms.heading")}
        onRowClick={(row) => setSelected(String(row.platform_id))}
        getRowId={(r) => String(r.platform_id)}
      />

      {selected && current && (
        <div className="space-y-6 rounded-lg border border-border p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">{String(current.name)}</h2>
            <LockdownControl
              platformId={selected}
              lockedDown={Boolean(current.locked_down)}
            />
          </div>
          <ErrorBoundary>
            <PlatformGroups platformId={selected} />
          </ErrorBoundary>
          <ErrorBoundary>
            <RuleEditor platformId={selected} />
          </ErrorBoundary>
          <ErrorBoundary>
            <ExposureBoard platformId={selected} />
          </ErrorBoundary>
        </div>
      )}
    </div>
  );
}

/**
 * The incident freeze. Distinct from `enabled` (which is "is this platform
 * in use at all") — lockdown means "stop all outbound provisioning right
 * now", and it records who froze it and why, so the reason field is
 * mandatory on the way in and ignored on the way out.
 */
function LockdownControl({
  platformId,
  lockedDown,
}: {
  platformId: string;
  lockedDown: boolean;
}) {
  const { t } = useTranslation();
  const lockdown = useLockdown(platformId);
  const [reason, setReason] = useState("");

  return (
    <div className="flex flex-wrap items-center gap-2">
      {!lockedDown && (
        <input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t("admin.platforms.reasonPlaceholder")}
          aria-label={t("admin.platforms.reasonPlaceholder")}
          className="h-8 w-56 rounded-md border border-border bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
        />
      )}
      <Button
        size="sm"
        variant={lockedDown ? "outline" : "destructive"}
        disabled={lockdown.isPending || (!lockedDown && reason.trim() === "")}
        onClick={() =>
          lockdown.mutate({ lockedDown: !lockedDown, reason: reason.trim() })
        }
        data-testid="lockdown-toggle"
      >
        {lockedDown
          ? t("admin.platforms.unlock")
          : t("admin.platforms.lock")}
      </Button>
    </div>
  );
}

/**
 * Creating a platform.
 *
 * `config` is sent as an empty object and there is no field for it: all
 * three drivers take their credentials from the environment
 * (HANGAR_DISCORD_*, HANGAR_TEAMSPEAK_*, HANGAR_MUMBLE_*), so
 * app.platform.config is NOT NULL and, in practice, empty. A textarea for a
 * column nothing reads would be an invitation to put a bot token somewhere
 * it is not encrypted.
 */
function NewPlatform() {
  const { t } = useTranslation();
  const create = useCreatePlatform();
  const [kind, setKind] = useState<string>(PLATFORM_KINDS[0]);
  const [name, setName] = useState("");

  return (
    <div className="flex flex-wrap items-end gap-2 rounded-lg border border-border p-3">
      <label className="text-sm">
        {t("admin.platforms.kind")}
        <select
          className="mt-1 block rounded-md border border-border bg-background px-2 py-1 text-sm"
          value={kind}
          data-testid="new-platform-kind"
          onChange={(e) => setKind(e.target.value)}
        >
          {PLATFORM_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
      </label>
      <label className="text-sm">
        {t("admin.platforms.name")}
        <Input
          className="mt-1 w-56"
          value={name}
          data-testid="new-platform-name"
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      <Button
        data-testid="create-platform"
        disabled={create.isPending || name.trim() === ""}
        onClick={() =>
          create.mutate(
            { kind, name: name.trim() },
            { onSuccess: () => setName("") },
          )
        }
      >
        {t("admin.platforms.create")}
      </Button>
      {/* The driver binds to platform rows at PROCESS START, so a platform
          created on a running installation has no driver until the next
          restart. Said here rather than left to be discovered as
          "provisioning does nothing for the platform I just made". */}
      <p className="w-full text-xs text-muted-foreground">
        {t("admin.platforms.restartNote")}
      </p>
    </div>
  );
}

/**
 * The remote groups an entitlement rule can target. A rule needs a
 * group_id, and until this phase nothing could create one — which made the
 * rule editor next door a form with no valid values for its most important
 * field on a fresh installation.
 */
function PlatformGroups({ platformId }: { platformId: string }) {
  const { t } = useTranslation();
  const groups = usePlatformGroups(platformId);
  const create = useCreatePlatformGroup(platformId);
  const [remoteRef, setRemoteRef] = useState("");
  const [name, setName] = useState("");

  const rows = groups.data?.rows ?? [];

  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold">{t("admin.platforms.groupsHeading")}</h3>
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("admin.platforms.noGroups")}</p>
      ) : (
        <ul className="space-y-1 text-sm" data-testid="platform-groups">
          {rows.map((group) => (
            <li key={String(group.group_id)}>
              {String(group.name)}{" "}
              <span className="font-mono text-xs text-muted-foreground">
                {String(group.remote_ref)}
              </span>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-end gap-2">
        <label className="text-sm">
          {t("admin.platforms.remoteRef")}
          <Input
            className="mt-1 w-56"
            value={remoteRef}
            data-testid="new-group-ref"
            onChange={(e) => setRemoteRef(e.target.value)}
          />
        </label>
        <label className="text-sm">
          {t("admin.platforms.name")}
          <Input
            className="mt-1 w-56"
            value={name}
            data-testid="new-group-name"
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <Button
          data-testid="create-group"
          disabled={create.isPending || remoteRef.trim() === "" || name.trim() === ""}
          onClick={() =>
            create.mutate(
              { remoteRef: remoteRef.trim(), name: name.trim() },
              {
                onSuccess: () => {
                  setRemoteRef("");
                  setName("");
                },
              },
            )
          }
        >
          {t("admin.platforms.create")}
        </Button>
      </div>
    </div>
  );
}
