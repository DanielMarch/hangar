import { createFileRoute } from "@tanstack/react-router";

import { SyncHealth } from "@/features/admin/sync/SyncHealth";

export const Route = createFileRoute("/_authed/admin/")({
  staticData: { breadcrumbKey: "admin.tabs.sync" },
  component: SyncHealth,
});
