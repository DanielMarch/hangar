import { createFileRoute } from "@tanstack/react-router";

import { CorporationStructures } from "@/features/corporation/CorporationStructures";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/structures",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationStructures corporationId={Number(corporationId)} />;
  },
});
