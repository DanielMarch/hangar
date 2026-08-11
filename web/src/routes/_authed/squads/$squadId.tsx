import {
  createFileRoute,
  Link,
  Outlet,
  useParams,
} from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { findSquad, squadsListQueryOptions } from "@/features/squads/queries";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authed/squads/$squadId")({
  loader: async ({ params, context }) => {
    try {
      const result = await context.queryClient.ensureQueryData(
        squadsListQueryOptions(),
      );
      const squad = findSquad(result.rows, params.squadId);
      const name =
        typeof squad?.name === "string" ? squad.name : params.squadId;
      return { breadcrumb: name };
    } catch {
      return { breadcrumb: params.squadId };
    }
  },
  component: SquadLayout,
});

const TABS = [
  { to: "/squads/$squadId/members", labelKey: "squads.tabs.members" },
  { to: "/squads/$squadId/moderators", labelKey: "squads.tabs.moderators" },
  { to: "/squads/$squadId/roles", labelKey: "squads.tabs.roles" },
  { to: "/squads/$squadId/applications", labelKey: "squads.tabs.applications" },
] as const;

function SquadLayout() {
  const { t } = useTranslation();
  const { squadId } = useParams({ from: "/_authed/squads/$squadId" });

  return (
    <div className="space-y-4">
      <nav className="flex flex-wrap gap-1 border-b border-border pb-2">
        {TABS.map((tab) => (
          <Link
            key={tab.to}
            to={tab.to}
            params={{ squadId }}
            className="rounded-md px-2.5 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            activeProps={{ className: cn("bg-accent text-cyan-400") }}
          >
            {t(tab.labelKey)}
          </Link>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
