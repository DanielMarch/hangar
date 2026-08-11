// The compatibility-pin advance flow — the screen Principle 12 is about.
//
// The pin decides which ESI routes the whole installation may call, so
// moving it is never a one-click action. The flow is strictly:
//
//   type a candidate date -> Preview (non-mutating) -> read the full diff
//   -> confirm -> Advance
//
// Two properties this component must hold, and the reasons neither is
// merely cosmetic:
//
//  1. The Advance button is unreachable until a preview for THAT EXACT
//     date has been read and confirmed. Editing the date after previewing
//     clears the confirmation — otherwise an operator could preview a
//     quiet week and advance across a noisy one.
//  2. A diff of "no routes changed" renders EXPLICITLY, as a sentence,
//     never as a blank panel. An administrator advancing across a quiet
//     week must be told "nothing changes" rather than be left to wonder
//     whether the preview failed to load (roadmap Phase 18 edge case; the
//     same empty-versus-unavailable distinction SRS §6 draws for
//     collections).
//
// The client-side D_max check below is a courtesy, not the guard: the
// server refuses an out-of-range candidate with a 422 whatever this
// component allows (internal/esi/catalogue.AdvancePin), because a UI-only
// bound check is bypassed by any direct API call.
import { AlertTriangle, ArrowRight, CheckCircle2, Lock, Unlock } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import {
  useAdvancePin,
  usePinPreview,
  type PinPreview,
  type RouteChange,
} from "@/features/admin/queries";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/api/client";

const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;

export function PinAdvance({ currentPin }: { currentPin?: string }) {
  const { t } = useTranslation();
  const [candidate, setCandidate] = useState("");
  // The date the loaded preview describes. Kept separate from `candidate`
  // so a preview never outlives the input it was taken for.
  const [previewedFor, setPreviewedFor] = useState<string | null>(null);
  const [confirmed, setConfirmed] = useState(false);

  const preview = usePinPreview();
  const advance = useAdvancePin();

  const wellFormed = DATE_PATTERN.test(candidate);
  const stale = previewedFor !== null && previewedFor !== candidate;
  const data = stale ? undefined : preview.data;

  function onCandidateChange(value: string) {
    setCandidate(value);
    // Any edit invalidates the confirmation. This is the property that
    // stops "preview something harmless, advance something else".
    setConfirmed(false);
    advance.reset();
  }

  function onPreview() {
    setConfirmed(false);
    preview.mutate(candidate, { onSuccess: () => setPreviewedFor(candidate) });
  }

  const canAdvance = Boolean(
    data && data.within_bounds && confirmed && !advance.isPending,
  );

  return (
    <section
      className="space-y-4 rounded-lg border border-border bg-card p-4"
      data-testid="pin-advance"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium">{t("admin.pin.heading")}</h3>
        {currentPin && (
          <Badge variant="outline" className="font-mono tabular-nums">
            {t("admin.pin.current", { date: currentPin })}
          </Badge>
        )}
      </div>

      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          {t("admin.pin.candidateLabel")}
          <input
            value={candidate}
            onChange={(e) => onCandidateChange(e.target.value)}
            placeholder={t("admin.pin.candidatePlaceholder")}
            aria-label={t("admin.pin.candidateLabel")}
            data-testid="pin-candidate"
            className="h-8 w-40 rounded-md border border-border bg-background px-2 font-mono text-sm tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
          />
        </label>
        <Button
          variant="outline"
          size="sm"
          disabled={!wellFormed || preview.isPending}
          onClick={onPreview}
          data-testid="pin-preview-button"
        >
          {preview.isPending ? t("admin.pin.previewing") : t("admin.pin.preview")}
        </Button>
      </div>

      {preview.error ? <PreviewFailed error={preview.error} /> : null}

      {stale && (
        <p className="text-sm text-amber-500" data-testid="pin-preview-stale">
          {t("admin.pin.stalePreview")}
        </p>
      )}

      {data && (
        <>
          <DiffPanel preview={data} />

          {!data.within_bounds && (
            <p
              className="flex items-start gap-2 text-sm text-destructive"
              data-testid="pin-out-of-range"
            >
              <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              {t("admin.pin.outOfRange", {
                dMax: data.d_max,
                candidate: data.candidate_pin,
              })}
            </p>
          )}

          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={confirmed}
              disabled={!data.within_bounds}
              onChange={(e) => setConfirmed(e.target.checked)}
              data-testid="pin-confirm"
              className="mt-0.5 size-4 accent-cyan-500"
            />
            <span>{t("admin.pin.confirmLabel")}</span>
          </label>

          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              variant="destructive"
              disabled={!canAdvance}
              onClick={() => advance.mutate(candidate)}
              data-testid="pin-advance-button"
            >
              {advance.isPending
                ? t("admin.pin.advancing")
                : t("admin.pin.advance")}
            </Button>
            {advance.isSuccess && (
              <span
                className="flex items-center gap-1 text-sm text-emerald-500"
                data-testid="pin-advanced"
              >
                <CheckCircle2 className="size-4" aria-hidden="true" />
                {t("admin.pin.advanced", { date: candidate })}
              </span>
            )}
            {advance.error ? <AdvanceFailed error={advance.error} /> : null}
          </div>
        </>
      )}
    </section>
  );
}

function PreviewFailed({ error }: { error: unknown }) {
  const { t } = useTranslation();
  const detail = error instanceof ApiError ? error.detail : undefined;
  return (
    <p className="text-sm text-destructive" data-testid="pin-preview-error">
      {detail ?? t("admin.pin.previewFailed")}
    </p>
  );
}

function AdvanceFailed({ error }: { error: unknown }) {
  const { t } = useTranslation();
  // A 422 here is the SERVER's own D_max refusal — surfaced verbatim,
  // because it carries the actual ceiling and this client's copy of the
  // bound may be stale.
  const detail = error instanceof ApiError ? error.detail : undefined;
  return (
    <span className="text-sm text-destructive" data-testid="pin-advance-error">
      {detail ?? t("admin.pin.advanceFailed")}
    </span>
  );
}

function DiffPanel({ preview }: { preview: PinPreview }) {
  const { t } = useTranslation();
  const { diff } = preview;
  const nothingChanges =
    diff.newly_unblocked.length === 0 && diff.newly_blocked.length === 0;

  return (
    <div className="space-y-3" data-testid="pin-diff">
      <p className="flex flex-wrap items-center gap-2 font-mono text-sm tabular-nums">
        <span>{diff.old_pin}</span>
        <ArrowRight className="size-3.5 shrink-0" aria-hidden="true" />
        <span className="font-semibold">{diff.new_pin}</span>
        <Badge variant="secondary">
          {t("admin.pin.dMax", { date: preview.d_max })}
        </Badge>
      </p>

      {nothingChanges ? (
        // NOT an empty state. Rendering a blank panel here invites
        // advancing the pin believing the preview simply failed to load.
        <p
          className="rounded-md border border-border bg-background p-3 text-sm text-muted-foreground"
          data-testid="pin-diff-no-change"
        >
          {t("admin.pin.noRoutesChange", { count: diff.unchanged })}
        </p>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          <ChangeList
            kind="unblocked"
            title={t("admin.pin.newlyUnblocked", {
              count: diff.newly_unblocked.length,
            })}
            routes={diff.newly_unblocked}
          />
          <ChangeList
            kind="blocked"
            title={t("admin.pin.newlyBlocked", {
              count: diff.newly_blocked.length,
            })}
            routes={diff.newly_blocked}
          />
        </div>
      )}
    </div>
  );
}

/**
 * One direction of the diff. Both directions always render, including the
 * empty one — "0 routes become blocked" is information an administrator
 * needs, and hiding the empty side would make a rollback (which only ever
 * populates the blocked side) look like a broken screen.
 */
function ChangeList({
  kind,
  title,
  routes,
}: {
  kind: "blocked" | "unblocked";
  title: string;
  routes: RouteChange[];
}) {
  const { t } = useTranslation();
  const Icon = kind === "blocked" ? Lock : Unlock;
  return (
    <div
      className="space-y-1 rounded-md border border-border bg-background p-3"
      data-testid={`pin-diff-${kind}`}
    >
      <p
        className={
          kind === "blocked"
            ? "flex items-center gap-1.5 text-sm font-medium text-destructive"
            : "flex items-center gap-1.5 text-sm font-medium text-emerald-500"
        }
      >
        <Icon className="size-3.5 shrink-0" aria-hidden="true" />
        {title}
      </p>
      {routes.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("admin.pin.none")}</p>
      ) : (
        <ul className="max-h-56 space-y-0.5 overflow-y-auto font-mono text-xs">
          {routes.map((r) => (
            <li key={r.operation_id} className="flex flex-wrap gap-1.5">
              <span className="text-muted-foreground">{r.method}</span>
              <span className="min-w-0 break-all">{r.upstream_path}</span>
              <span className="text-muted-foreground tabular-nums">
                {r.compatibility_date}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
