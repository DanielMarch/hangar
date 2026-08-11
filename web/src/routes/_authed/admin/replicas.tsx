import { createFileRoute } from "@tanstack/react-router";

import { Replicas } from "@/features/admin/esi/Replicas";

export const Route = createFileRoute("/_authed/admin/replicas")({
  staticData: { breadcrumbKey: "admin.tabs.replicas" },
  component: Replicas,
});
