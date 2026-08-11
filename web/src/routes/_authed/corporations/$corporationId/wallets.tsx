import { createFileRoute } from "@tanstack/react-router";

import { CorporationWallets } from "@/features/corporation/CorporationWallets";

export const Route = createFileRoute(
  "/_authed/corporations/$corporationId/wallets",
)({
  component: () => {
    const { corporationId } = Route.useParams();
    return <CorporationWallets corporationId={Number(corporationId)} />;
  },
});
