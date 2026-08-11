import { createFileRoute } from "@tanstack/react-router";

import { SecurityLog } from "@/features/admin/users/Users";

export const Route = createFileRoute("/_authed/admin/security")({
  staticData: { breadcrumbKey: "admin.tabs.security" },
  component: SecurityLog,
});
