import { createFileRoute } from "@tanstack/react-router";

import { AlertRouting } from "@/features/admin/alerts/AlertRouting";

export const Route = createFileRoute("/_authed/admin/routing")({
  staticData: { breadcrumbKey: "admin.tabs.routing" },
  component: AlertRouting,
});
