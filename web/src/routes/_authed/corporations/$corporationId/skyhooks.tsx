import { createFileRoute } from "@tanstack/react-router";

import { CorporationSkyhooks } from "@/features/corporation/CorporationSkyhooks";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/skyhooks",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationSkyhooks corporationId={Number(corporationId)} />;
  },
});
