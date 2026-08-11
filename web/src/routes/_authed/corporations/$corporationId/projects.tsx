import { createFileRoute } from "@tanstack/react-router";

import { CorporationProjects } from "@/features/corporation/CorporationProjects";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/projects",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationProjects corporationId={Number(corporationId)} />;
  },
});
