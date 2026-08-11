import { createFileRoute } from "@tanstack/react-router";

import { CorporationStarbases } from "@/features/corporation/CorporationStarbases";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/starbases",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationStarbases corporationId={Number(corporationId)} />;
  },
});
