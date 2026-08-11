// User administration and the append-only security log.
//
// The security log is filterable by user because that is how it is
// actually read — "what happened to this account" — and the API takes the
// filter as a query parameter rather than the client filtering a page it
// happens to hold.
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useCollection } from "@/api/queries/collection";
import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, textColumn } from "@/components/data-table/columns";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { usersPath } from "@/features/admin/queries";

export function Users() {
  const { t } = useTranslation();
  return (
    <ErrorBoundary>
      <AutoCollectionTab
        path={usersPath}
        init={{}}
        queryKey={["admin", "users"]}
        title={t("admin.users.heading")}
        rowIdKey="user_id"
      />
    </ErrorBoundary>
  );
}

export function SecurityLog() {
  const { t } = useTranslation();
  const [userId, setUserId] = useState("");
  // Only send the filter once it is a plausible uuid — a half-typed one is
  // a 400 on every keystroke otherwise.
  const applied = /^[0-9a-fA-F-]{36}$/.test(userId) ? userId : "";
  const query = useCollection(
    "/api/v1/admin/security-log",
    { params: { query: applied ? { user_id: applied } : {} } },
    ["admin", "security-log", applied],
  );

  return (
    <div className="space-y-2">
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        {t("admin.security.filterByUser")}
        <input
          value={userId}
          onChange={(e) => setUserId(e.target.value)}
          placeholder={t("admin.security.userIdPlaceholder")}
          aria-label={t("admin.security.filterByUser")}
          className="h-8 w-80 rounded-md border border-border bg-background px-2 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
        />
      </label>
      <CollectionTable
        query={query}
        columns={[
          dateColumn("at", t("admin.security.occurredAt")),
          textColumn("action", t("admin.security.action")),
          textColumn("user_id", t("admin.security.actor")),
          textColumn("target", t("admin.security.target")),
          textColumn("ip_address", t("admin.security.ip")),
        ]}
        title={t("admin.security.heading")}
        getRowId={(r, i) => String(r.log_id ?? i)}
      />
    </div>
  );
}
