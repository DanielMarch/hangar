import { createFileRoute } from "@tanstack/react-router";

import { RateLimits } from "@/features/admin/esi/RateLimits";

export const Route = createFileRoute("/_authed/admin/ratelimits")({
  staticData: { breadcrumbKey: "admin.tabs.rateLimits" },
  component: RateLimits,
});
