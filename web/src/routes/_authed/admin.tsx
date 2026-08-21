// The admin section's layout route. `staticData.breadcrumbKey` is all a
// route needs to earn its crumb — Breadcrumbs.tsx walks useMatches() and
// resolves it, and no page component here (or anywhere) renders a
// breadcrumb itself (SRS §8.2).
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authed/admin")({
  staticData: { breadcrumbKey: "nav.admin" },
  component: AdminLayout,
});

const TABS = [
  { to: "/admin", labelKey: "admin.tabs.sync", exact: true },
  { to: "/admin/routes", labelKey: "admin.tabs.routes" },
  { to: "/admin/esi", labelKey: "admin.tabs.esi" },
  { to: "/admin/ratelimits", labelKey: "admin.tabs.rateLimits" },
  { to: "/admin/replicas", labelKey: "admin.tabs.replicas" },
  { to: "/admin/scopes", labelKey: "admin.tabs.scopes" },
  { to: "/admin/platforms", labelKey: "admin.tabs.platforms" },
  { to: "/admin/alerts", labelKey: "admin.tabs.alerts" },
  // PHASE 23 (N-4). Three tabs for three surfaces that had store queries
  // and no screen: §4.4's routing configuration, the open-vocabulary board
  // Gate 6 fills, and §4.9's webhook boards.
  { to: "/admin/routing", labelKey: "admin.tabs.routing" },
  { to: "/admin/vocabularies", labelKey: "admin.tabs.vocabularies" },
  { to: "/admin/webhooks", labelKey: "admin.tabs.webhooks" },
  { to: "/admin/users", labelKey: "admin.tabs.users" },
  { to: "/admin/security", labelKey: "admin.tabs.security" },
] as const;

function AdminLayout() {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      <nav className="flex flex-wrap gap-1 border-b border-border pb-2">
        {TABS.map((tab) => (
          <Link
            key={tab.to}
            to={tab.to}
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
