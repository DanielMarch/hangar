import { createFileRoute } from "@tanstack/react-router";

import { CorporationLedgers } from "@/features/corporation/CorporationLedgers";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/ledgers",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationLedgers corporationId={Number(corporationId)} />;
  },
});
