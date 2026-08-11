import { createFileRoute } from "@tanstack/react-router";

import { CorporationMembers } from "@/features/corporation/CorporationMembers";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/members",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationMembers corporationId={Number(corporationId)} />;
  },
});
