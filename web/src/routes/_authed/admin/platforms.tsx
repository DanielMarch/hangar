import { createFileRoute } from "@tanstack/react-router";

import { Platforms } from "@/features/admin/provisioning/Platforms";

export const Route = createFileRoute("/_authed/admin/platforms")({
  staticData: { breadcrumbKey: "admin.tabs.platforms" },
  component: Platforms,
});
