// SRS §5.2: most /api/v1/characters, /corporations, /squads sub-resources
// name a specific RBAC permission; a 403 from those is EXPECTED for
// ordinary members (they can see their own characters' wallet but not a
// squadmate's, say) and must degrade gracefully — never render as a crash
// or a generic error-boundary retry panel. Only /api/v1/me* and
// /api/v1/meta/* (Phase 16) skip RBAC entirely.
import { ShieldAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

import { ApiError } from "@/api/client";

// eslint-disable-next-line react-refresh/only-export-components -- isForbidden and <Forbidden/> are a deliberate, always-used-together pair (every 403 check needs both).
export function isForbidden(error: unknown): error is ApiError {
  return error instanceof ApiError && error.status === 403;
}

export function Forbidden({ detail }: { detail?: string }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-start gap-1 rounded-md border border-border bg-card p-4">
      <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
        <ShieldAlert className="size-4 shrink-0" aria-hidden="true" />
        {t("errors.forbiddenTitle")}
      </div>
      <p className="text-sm text-muted-foreground">
        {detail ?? t("errors.forbiddenBody")}
      </p>
    </div>
  );
}
