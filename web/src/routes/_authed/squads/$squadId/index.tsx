import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/squads/$squadId/")({
  component: () => {
    const { squadId } = Route.useParams();
    return <Navigate to="/squads/$squadId/members" params={{ squadId }} />;
  },
});
