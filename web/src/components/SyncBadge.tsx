// Renders the `_sync` envelope every collection/item response carries
// (last_modified_at, next_due_at, stale, blocked_by_pin). Per the Phase 16
// brief: `blocked_by_pin` set means `data` is `null`, not `[]` — this badge
// is what tells the member (and the admin reading over their shoulder) WHY,
// with the administrator-facing explanation the API sent, rather than
// letting the surrounding view quietly render an empty table. Empty and
// unavailable are different states and must never collapse into each other.
import { formatDistanceToNow } from "date-fns";
import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { Sync } from "@/api/queries/status";

export function SyncBadge({ sync }: { sync: Sync }) {
  const { t } = useTranslation();

  if (sync.blocked_by_pin) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge variant="destructive">{t("sync.unavailable")}</Badge>
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">
          {sync.blocked_by_pin}
        </TooltipContent>
      </Tooltip>
    );
  }

  if (!sync.last_modified_at) {
    return <Badge variant="secondary">{t("sync.neverSynced")}</Badge>;
  }

  const modified = new Date(sync.last_modified_at);
  const label = t("sync.lastUpdated", {
    time: formatDistanceToNow(modified, { addSuffix: true }),
  });

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant={sync.stale ? "outline" : "secondary"}
          className="font-mono tabular-nums"
        >
          {sync.stale ? `${t("sync.stale")} · ` : ""}
          {label}
        </Badge>
      </TooltipTrigger>
      <TooltipContent>
        {sync.next_due_at
          ? t("sync.lastUpdated", {
              time: formatDistanceToNow(new Date(sync.next_due_at), {
                addSuffix: true,
              }),
            })
          : label}
      </TooltipContent>
    </Tooltip>
  );
}
