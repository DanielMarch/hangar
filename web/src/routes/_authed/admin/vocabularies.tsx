import { createFileRoute } from "@tanstack/react-router";

import { Vocabularies } from "@/features/admin/vocabularies/Vocabularies";

export const Route = createFileRoute("/_authed/admin/vocabularies")({
  staticData: { breadcrumbKey: "admin.tabs.vocabularies" },
  component: Vocabularies,
});
