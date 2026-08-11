import { createFileRoute } from "@tanstack/react-router";

import { SquadMembers } from "@/features/squads/SquadMembers";

export const Route = createFileRoute("/_authed/squads/$squadId/members")({
  component: () => {
    const { squadId } = Route.useParams();
    return <SquadMembers squadId={squadId} />;
  },
});
