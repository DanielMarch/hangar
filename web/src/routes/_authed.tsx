// Pathless layout route (the `_` prefix carries no URL segment — TanStack
// Router file-based routing convention) gating every authenticated screen.
// `beforeLoad` resolves the shared `meQueryOptions` cache entry
// (web/src/api/queries/me.ts) via the router-context QueryClient; a 401
// bounces to /login before AppShell — and any query the nested route would
// have fired — ever mounts. GET /api/v1/me needs only a resolved session,
// no RBAC permission (SRS §5.2), so this is the one and only gate every
// signed-in member clears.
import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";

import { meQueryOptions } from "@/api/queries/me";
import { AppShell } from "@/components/layout/AppShell";

export const Route = createFileRoute("/_authed")({
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.ensureQueryData(meQueryOptions);
    } catch {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
  component: AppShellLayout,
});

function AppShellLayout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}
