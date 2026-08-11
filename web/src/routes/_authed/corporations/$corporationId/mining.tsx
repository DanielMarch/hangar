import { createFileRoute } from "@tanstack/react-router";

import { CorporationMining } from "@/features/corporation/CorporationMining";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/mining",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationMining corporationId={Number(corporationId)} />;
  },
});
