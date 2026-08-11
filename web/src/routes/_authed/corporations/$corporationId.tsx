import {
  createFileRoute,
  Link,
  Outlet,
  useParams,
} from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { corporationQueryOptions } from "@/features/corporation/queries";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authed/corporations/$corporationId")({
  loader: async ({ params, context }) => {
    try {
      const result = await context.queryClient.ensureQueryData(
        corporationQueryOptions(Number(params.corporationId)),
      );
      const name =
        typeof result.data?.name === "string"
          ? result.data.name
          : params.corporationId;
      return { breadcrumb: name };
    } catch {
      return { breadcrumb: params.corporationId };
    }
  },
  component: CorporationLayout,
});

const TABS = [
  {
    to: "/corporations/$corporationId",
    labelKey: "corporations.tabs.sheet",
    exact: true,
  },
  {
    to: "/corporations/$corporationId/members",
    labelKey: "corporations.tabs.members",
  },
  {
    to: "/corporations/$corporationId/wallets",
    labelKey: "corporations.tabs.wallets",
  },
  {
    to: "/corporations/$corporationId/ledgers",
    labelKey: "corporations.tabs.ledgers",
  },
  {
    to: "/corporations/$corporationId/structures",
    labelKey: "corporations.tabs.structures",
  },
  {
    to: "/corporations/$corporationId/starbases",
    labelKey: "corporations.tabs.starbases",
  },
  {
    to: "/corporations/$corporationId/skyhooks",
    labelKey: "corporations.tabs.skyhooks",
  },
  {
    to: "/corporations/$corporationId/projects",
    labelKey: "corporations.tabs.projects",
  },
  {
    to: "/corporations/$corporationId/mining",
    labelKey: "corporations.tabs.mining",
  },
] as const;

function CorporationLayout() {
  const { t } = useTranslation();
  const { corporationId } = useParams({
    from: "/_authed/corporations/$corporationId",
  });

  return (
    <div className="space-y-4">
      <nav className="flex flex-wrap gap-1 border-b border-border pb-2">
        {TABS.map((tab) => (
          <Link
            key={tab.to}
            to={tab.to}
            params={{ corporationId }}
            activeOptions={{ exact: "exact" in tab && tab.exact }}
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
