// The blocked-by-pin board plus the pin-advance flow it exists to drive.
// No legacy equivalent — this board exists to surface an SRS invariant
// (Principle 12: a route newer than the app pin is never called, and is
// visible as blocked rather than silently missing).
import { useTranslation } from "react-i18next";

import { AutoCollectionTab } from "@/components/data-table/AutoCollectionTab";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { CardSkeleton } from "@/components/Skeleton";
import { PinAdvance } from "@/features/admin/esi/PinAdvance";
import { PinHistory } from "@/features/admin/esi/PinHistory";
import { blockedRoutesPath, syncHealthQueryOptions } from "@/features/admin/queries";
import { useQuery } from "@tanstack/react-query";

export function BlockedByPin() {
  const { t } = useTranslation();
  // Sync health carries the current pin alongside the route counts, so the
  // advance flow can show "you are here" without a second endpoint.
  const health = useQuery(syncHealthQueryOptions());
  const currentPin =
    typeof health.data?.data?.compatibility_pin === "string"
      ? health.data.data.compatibility_pin
      : undefined;

  return (
    <div className="space-y-6">
      <ErrorBoundary>
        {health.isPending ? <CardSkeleton /> : <PinAdvance currentPin={currentPin} />}
      </ErrorBoundary>

      <ErrorBoundary>
        <AutoCollectionTab
          path={blockedRoutesPath}
          init={{}}
          queryKey={["admin", "esi", "blocked"]}
          title={t("admin.esi.blockedHeading")}
          rowIdKey="route_id"
          exclude={["spec_fragment", "identifier_types"]}
        />
      </ErrorBoundary>

      <ErrorBoundary>
        <PinHistory />
      </ErrorBoundary>
    </div>
  );
}
