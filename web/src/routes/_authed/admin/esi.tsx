import { createFileRoute } from "@tanstack/react-router";

import { BlockedByPin } from "@/features/admin/esi/BlockedByPin";

export const Route = createFileRoute("/_authed/admin/esi")({
  staticData: { breadcrumbKey: "admin.tabs.esi" },
  component: BlockedByPin,
});
