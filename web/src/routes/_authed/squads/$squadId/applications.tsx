import { createFileRoute } from "@tanstack/react-router";

import { SquadApplications } from "@/features/squads/SquadApplications";

export const Route = createFileRoute("/_authed/squads/$squadId/applications")({
  component: () => {
    const { squadId } = Route.useParams();
    return <SquadApplications squadId={squadId} />;
  },
});
