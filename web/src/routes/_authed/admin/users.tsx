import { createFileRoute } from "@tanstack/react-router";

import { Users } from "@/features/admin/users/Users";

export const Route = createFileRoute("/_authed/admin/users")({
  staticData: { breadcrumbKey: "admin.tabs.users" },
  component: Users,
});
