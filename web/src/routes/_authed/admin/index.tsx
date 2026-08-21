import { createFileRoute } from "@tanstack/react-router";

import { ErrorBoundary } from "@/components/ErrorBoundary";
import { SubscriptionManager } from "@/features/admin/sync/SubscriptionManager";
import { SyncHealth } from "@/features/admin/sync/SyncHealth";

export const Route = createFileRoute("/_authed/admin/")({
  staticData: { breadcrumbKey: "admin.tabs.sync" },
  component: SyncPage,
});

// PHASE 23 (N-4). The subscription manager sits under sync health rather
// than on a tab of its own, because it answers the follow-up question that
// screen provokes: health says how many subscriptions are unhealthy, and
// this is where an operator goes to do something about one.
function SyncPage() {
  return (
    <div className="space-y-8">
      <SyncHealth />
      <ErrorBoundary>
        <SubscriptionManager />
      </ErrorBoundary>
    </div>
  );
}
