import { createFileRoute } from "@tanstack/react-router";

import { SquadModerators } from "@/features/squads/SquadModerators";

export const Route = createFileRoute("/_authed/squads/$squadId/moderators")({
  component: () => {
    const { squadId } = Route.useParams();
    return <SquadModerators squadId={squadId} />;
  },
});
