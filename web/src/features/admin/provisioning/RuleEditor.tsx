// The entitlement rule editor.
//
// SAVING WITHOUT PREVIEWING IS IMPOSSIBLE, and not only because the button
// is disabled. POST .../rules/preview returns a `preview_token` derived
// from the platform and the exact rule set; PUT .../rules requires it back
// and recomputes it over the rules actually submitted, refusing a mismatch
// with a 422. So the gate holds for any client, including one that never
// renders this screen — the disabled button is the courteous half, the
// token is the enforcing half.
//
// Editing any rule after previewing therefore doesn't merely grey the
// button out: the token this component holds stops matching, and the
// server would refuse the save even if the button were forced. That is the
// case worth engineering for — previewing something harmless and saving
// something else is how an accidental mass revocation happens.
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { ApiError } from "@/api/client";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { TableSkeleton } from "@/components/Skeleton";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  asRuleEffect,
  RULE_EFFECTS,
  usePlatformGroups,
  usePlatformRules,
  usePreviewRules,
  useSaveRules,
  type RuleDraft,
} from "@/features/admin/queries";

/** internal/provisioning/entitlement's closed Go-side set, mirrored. */
const SOURCE_KINDS = [
  "user",
  "role",
  "corporation",
  "alliance",
  "corp_title",
  "squad",
  "public",
] as const;

/**
 * Canonical, order-insensitive identity of a rule set. Two drafts with the
 * same rules in a different order are the same set and the server's token
 * agrees, so re-ordering rows must not invalidate a preview.
 */
function ruleSetKey(rules: RuleDraft[]): string {
  return rules
    .map((r) => [r.source_kind, r.source_ref, r.group_id, r.effect].join("\t"))
    .sort()
    .join("\n");
}

export function RuleEditor({ platformId }: { platformId: string }) {
  const { t } = useTranslation();
  const groups = usePlatformGroups(platformId);
  const existing = usePlatformRules(platformId);
  const preview = usePreviewRules(platformId);
  const save = useSaveRules(platformId);

  const [draft, setDraft] = useState<RuleDraft[] | null>(null);
  // The rule set the held token was issued for. Compared by value, not by
  // reference, so re-ordering is not treated as an edit.
  const [previewedKey, setPreviewedKey] = useState<string | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [confirmed, setConfirmed] = useState(false);

  const groupRows = groups.data?.rows ?? [];
  const rules =
    draft ??
    (existing.data?.rows ?? []).map<RuleDraft>((r) => ({
      source_kind: String(r.source_kind ?? "public"),
      source_ref: String(r.source_ref ?? ""),
      group_id: String(r.group_id ?? ""),
      effect: asRuleEffect(String(r.effect ?? "grant")),
    }));

  const currentKey = ruleSetKey(rules);
  // A token is only usable for the rule set it was issued for. This is the
  // client-side mirror of the server's own check.
  const previewValid = token !== null && previewedKey === currentKey;
  const canSave = previewValid && confirmed && !save.isPending;

  function edit(next: RuleDraft[]) {
    setDraft(next);
    // Any edit invalidates the confirmation. The token and the key it was
    // issued for are deliberately KEPT: `previewValid` goes false on its
    // own because the key no longer matches, which is what makes the
    // "these rules changed since you previewed them" hint renderable at
    // all. Clearing the token here instead would leave the operator with a
    // silently-disabled Save button and no explanation — and the held
    // token is harmless, since the server recomputes the digest over the
    // rules actually submitted and would refuse it too.
    setConfirmed(false);
    save.reset();
  }

  function onPreview() {
    setConfirmed(false);
    const key = ruleSetKey(rules);
    preview.mutate(rules, {
      onSuccess: (result) => {
        setToken(result.preview_token);
        setPreviewedKey(key);
      },
    });
  }

  if (groups.isPending || existing.isPending) return <TableSkeleton rows={4} />;

  return (
    <section className="space-y-4" data-testid="rule-editor">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium">{t("admin.rules.heading")}</h3>
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            edit([
              ...rules,
              {
                source_kind: "public",
                source_ref: "",
                group_id: String(groupRows[0]?.group_id ?? ""),
                effect: "grant",
              },
            ])
          }
          disabled={groupRows.length === 0}
          data-testid="rule-add"
        >
          <Plus className="size-3.5" aria-hidden="true" />
          {t("admin.rules.add")}
        </Button>
      </div>

      {groupRows.length === 0 && (
        <p className="text-sm text-muted-foreground">
          {t("admin.rules.noGroups")}
        </p>
      )}

      {rules.length === 0 ? (
        <p className="text-sm text-muted-foreground" data-testid="rule-empty">
          {t("admin.rules.empty")}
        </p>
      ) : (
        <ul className="space-y-2">
          {rules.map((rule, i) => (
            <li
              key={i}
              className="flex flex-wrap items-end gap-2 rounded-md border border-border bg-card p-3"
              data-testid="rule-row"
            >
              <Field label={t("admin.rules.sourceKind")}>
                <select
                  value={rule.source_kind}
                  aria-label={t("admin.rules.sourceKind")}
                  data-testid="rule-source-kind"
                  onChange={(e) =>
                    edit(
                      rules.map((r, j) =>
                        j === i ? { ...r, source_kind: e.target.value } : r,
                      ),
                    )
                  }
                  className="h-8 rounded-md border border-border bg-background px-2 text-sm"
                >
                  {SOURCE_KINDS.map((k) => (
                    <option key={k} value={k}>
                      {k}
                    </option>
                  ))}
                </select>
              </Field>

              <Field label={t("admin.rules.sourceRef")}>
                <input
                  value={rule.source_ref}
                  aria-label={t("admin.rules.sourceRef")}
                  data-testid="rule-source-ref"
                  onChange={(e) =>
                    edit(
                      rules.map((r, j) =>
                        j === i ? { ...r, source_ref: e.target.value } : r,
                      ),
                    )
                  }
                  className="h-8 w-48 rounded-md border border-border bg-background px-2 font-mono text-sm"
                />
              </Field>

              <Field label={t("admin.rules.group")}>
                <select
                  value={rule.group_id}
                  aria-label={t("admin.rules.group")}
                  data-testid="rule-group"
                  onChange={(e) =>
                    edit(
                      rules.map((r, j) =>
                        j === i ? { ...r, group_id: e.target.value } : r,
                      ),
                    )
                  }
                  className="h-8 rounded-md border border-border bg-background px-2 text-sm"
                >
                  {groupRows.map((g) => (
                    <option key={String(g.group_id)} value={String(g.group_id)}>
                      {String(g.name ?? g.group_id)}
                    </option>
                  ))}
                </select>
              </Field>

              <Field label={t("admin.rules.effect")}>
                <select
                  value={rule.effect}
                  aria-label={t("admin.rules.effect")}
                  data-testid="rule-effect"
                  onChange={(e) =>
                    edit(
                      rules.map((r, j) =>
                        j === i
                          ? { ...r, effect: asRuleEffect(e.target.value) }
                          : r,
                      ),
                    )
                  }
                  className="h-8 rounded-md border border-border bg-background px-2 text-sm"
                >
                  {RULE_EFFECTS.map((e) => (
                    <option key={e} value={e}>
                      {e}
                    </option>
                  ))}
                </select>
              </Field>

              <Button
                size="sm"
                variant="ghost"
                aria-label={t("admin.rules.remove")}
                data-testid="rule-remove"
                onClick={() => edit(rules.filter((_, j) => j !== i))}
              >
                <Trash2 className="size-3.5" aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={preview.isPending}
          onClick={onPreview}
          data-testid="rule-preview-button"
        >
          {preview.isPending
            ? t("admin.rules.previewing")
            : t("admin.rules.preview")}
        </Button>
        {token !== null && !previewValid && (
          <span className="text-sm text-amber-500" data-testid="rule-preview-stale">
            {t("admin.rules.stalePreview")}
          </span>
        )}
      </div>

      {preview.error ? (
        <p className="text-sm text-destructive">
          {preview.error instanceof ApiError
            ? (preview.error.detail ?? preview.error.message)
            : t("admin.rules.previewFailed")}
        </p>
      ) : null}

      {previewValid && preview.data && (
        <ErrorBoundary>
          <PreviewPanel
            diffs={preview.data.diffs}
            confirmed={confirmed}
            onConfirm={setConfirmed}
          />
        </ErrorBoundary>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          disabled={!canSave}
          onClick={() => {
            if (token) save.mutate({ rules, previewToken: token });
          }}
          data-testid="rule-save-button"
        >
          {save.isPending ? t("admin.rules.saving") : t("admin.rules.save")}
        </Button>
        {!previewValid && (
          <span className="text-xs text-muted-foreground" data-testid="rule-save-blocked">
            {t("admin.rules.mustPreview")}
          </span>
        )}
        {save.isSuccess && (
          <span className="text-sm text-emerald-500" data-testid="rule-saved">
            {t("admin.rules.saved")}
          </span>
        )}
        {save.error ? (
          <span className="text-sm text-destructive" data-testid="rule-save-error">
            {save.error instanceof ApiError
              ? (save.error.detail ?? save.error.message)
              : t("admin.rules.saveFailed")}
          </span>
        ) : null}
      </div>
    </section>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      {children}
    </label>
  );
}

/**
 * What the hypothetical rule set would do, per user. Rendered BEFORE the
 * save is reachable — an empty diff ("nobody is affected") is stated
 * explicitly for the same reason the pin diff states it: a blank panel
 * reads as a failed load, and an operator who assumes the preview broke
 * learns nothing from it.
 */
function PreviewPanel({
  diffs,
  confirmed,
  onConfirm,
}: {
  diffs: { user_id: string; gained: string[] | null; lost: string[] | null }[];
  confirmed: boolean;
  onConfirm: (v: boolean) => void;
}) {
  const { t } = useTranslation();
  const losing = diffs.filter((d) => (d.lost?.length ?? 0) > 0);
  const gaining = diffs.filter((d) => (d.gained?.length ?? 0) > 0);

  return (
    <div
      className="space-y-3 rounded-md border border-border bg-card p-4"
      data-testid="rule-preview"
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={losing.length > 0 ? "destructive" : "secondary"}>
          {t("admin.rules.losing", { count: losing.length })}
        </Badge>
        <Badge variant="secondary">
          {t("admin.rules.gaining", { count: gaining.length })}
        </Badge>
      </div>

      {diffs.length === 0 ? (
        <p className="text-sm text-muted-foreground" data-testid="rule-preview-no-change">
          {t("admin.rules.noUsersAffected")}
        </p>
      ) : (
        <ul className="max-h-56 space-y-1 overflow-y-auto font-mono text-xs">
          {diffs.map((d) => (
            <li key={d.user_id} className="flex flex-wrap gap-2">
              <span className="text-muted-foreground">{d.user_id}</span>
              {(d.gained?.length ?? 0) > 0 && (
                <span className="text-emerald-500">+{d.gained?.join(", ")}</span>
              )}
              {(d.lost?.length ?? 0) > 0 && (
                <span className="text-destructive">-{d.lost?.join(", ")}</span>
              )}
            </li>
          ))}
        </ul>
      )}

      <label className="flex items-start gap-2 text-sm">
        <input
          type="checkbox"
          checked={confirmed}
          onChange={(e) => onConfirm(e.target.checked)}
          data-testid="rule-confirm"
          className="mt-0.5 size-4 accent-cyan-500"
        />
        <span>{t("admin.rules.confirmLabel")}</span>
      </label>
    </div>
  );
}
