import { createFileRoute } from "@tanstack/react-router";

import { WebhookBoards } from "@/features/admin/webhooks/WebhookBoards";

export const Route = createFileRoute("/_authed/admin/webhooks")({
  staticData: { breadcrumbKey: "admin.tabs.webhooks" },
  component: WebhookBoards,
});
