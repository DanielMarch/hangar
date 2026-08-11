import { createFileRoute } from "@tanstack/react-router";

import { CorporationSheet } from "@/features/corporation/CorporationSheet";

export const Route = createFileRoute("/_authed/corporations/$corporationId/")({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationSheet corporationId={Number(corporationId)} />;
  },
});
