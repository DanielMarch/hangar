// Router root. Holds the QueryClient in router context (so route
// `beforeLoad` guards — see _authed.tsx — can call
// `context.queryClient.ensureQueryData(...)` without a second provider) and
// nothing else: all real chrome lives in _authed.tsx's AppShell so the
// unauthenticated /login route isn't forced to render a sidebar.
import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";

interface RouterContext {
  queryClient: QueryClient;
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootComponent,
});

function RootComponent() {
  return <Outlet />;
}
