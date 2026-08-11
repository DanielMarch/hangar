// Phase 16 exit criterion `TestBreadcrumbsDerivedFromRouter`: builds a
// small, purpose-made nested route tree (independent of how many real app
// routes exist yet) where each level sets `staticData.breadcrumbKey` in its
// own route file — exactly the pattern web/src/routes/_authed/index.tsx
// uses — and asserts Breadcrumbs renders the full chain having been given
// nothing but the router. No page ever constructs a `<Breadcrumbs
// items={...}/>` itself.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/i18n";
import { Breadcrumbs } from "./Breadcrumbs";

function buildRouter(initialPath: string) {
  const rootRoute = createRootRoute({ component: () => <Breadcrumbs /> });

  // Level 1: a pathless-in-spirit ancestor with its own crumb.
  const squadsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "squads",
    staticData: { breadcrumbKey: "nav.squads" },
  });

  // Level 2: nested under level 1, own crumb, own URL segment — proves the
  // FULL CHAIN renders, not just the leaf.
  const detailRoute = createRoute({
    getParentRoute: () => squadsRoute,
    path: "$squadId",
    staticData: { breadcrumbKey: "nav.dashboard" },
  });

  const routeTree = rootRoute.addChildren([
    squadsRoute.addChildren([detailRoute]),
  ]);
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });
  return router;
}

describe("Breadcrumbs", () => {
  it("derives the full crumb chain from router state alone for a nested route", async () => {
    const router = buildRouter("/squads/123");
    const queryClient = new QueryClient();

    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );

    // Both ancestor and leaf crumbs must appear — the whole chain, derived
    // purely from each matched route's own staticData, not hand-assembled.
    expect(await screen.findByText("Squads")).not.toBeNull();
    expect(await screen.findByText("Dashboard")).not.toBeNull();
  });

  it("renders nothing when no matched route declares a breadcrumbKey", async () => {
    const rootOnly = createRootRoute({ component: () => <Breadcrumbs /> });
    const router = createRouter({
      routeTree: rootOnly,
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    const queryClient = new QueryClient();

    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(container.querySelector("nav")).toBeNull();
  });
});
