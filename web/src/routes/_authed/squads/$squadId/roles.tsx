import { createFileRoute } from "@tanstack/react-router";

import { SquadRoles } from "@/features/squads/SquadRoles";

export const Route = createFileRoute("/_authed/squads/$squadId/roles")({
  component: () => {
    const { squadId } = Route.useParams();
    return <SquadRoles squadId={squadId} />;
  },
});
