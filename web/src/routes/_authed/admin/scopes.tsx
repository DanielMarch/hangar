import { createFileRoute } from "@tanstack/react-router";

import { UnknownScopes } from "@/features/admin/scopes/UnknownScopes";

export const Route = createFileRoute("/_authed/admin/scopes")({
  staticData: { breadcrumbKey: "admin.tabs.scopes" },
  component: UnknownScopes,
});
