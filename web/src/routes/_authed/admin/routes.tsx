import { createFileRoute } from "@tanstack/react-router";

import { RouteCatalogue } from "@/features/admin/sync/RouteCatalogue";

export const Route = createFileRoute("/_authed/admin/routes")({
  staticData: { breadcrumbKey: "admin.tabs.routes" },
  component: RouteCatalogue,
});
