import { createFileRoute } from "@tanstack/react-router";

import { AlertBoards } from "@/features/admin/alerts/AlertBoards";

export const Route = createFileRoute("/_authed/admin/alerts")({
  staticData: { breadcrumbKey: "admin.tabs.alerts" },
  component: AlertBoards,
});
