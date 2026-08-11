// Exact-age arithmetic for the exposure board, in its own module so the
// board component can stay component-only (react-refresh) and so the
// arithmetic is unit-testable without rendering a table.
//
// Both functions take the reference instant as a parameter rather than
// calling Date.now() themselves: the whole point of the exposure board's
// exit criterion is that an age is measured from a specific, named moment
// (`event_at`) rather than from whenever the code happened to run.

/**
 * Whole seconds elapsed since `eventAt`, or null when the row carries no
 * event timestamp.
 *
 * `eventAt` is `app.provisioning_audit.event_at` — the moment the
 * triggering condition became TRUE — and never a job-start or
 * last-reconciled time. Gate 2 measures revocation latency as
 * `platform_call_completed_at - event_at`, so a board that aged rows from
 * job start would under-report exactly the exposure the gate bounds, and
 * would under-report it worst precisely when the queue is backed up.
 *
 * Clamped at zero: a row whose event_at is slightly in the future (clock
 * skew between the API server and whatever enqueued it) is "0s", not a
 * negative age.
 */
export function ageSeconds(eventAt: unknown, now: number): number | null {
  if (typeof eventAt !== "string") return null;
  const t = Date.parse(eventAt);
  if (Number.isNaN(t)) return null;
  return Math.max(0, Math.floor((now - t) / 1000));
}

/**
 * `1h 04m 09s` — exact, never "about an hour ago". An exposure board is
 * read to answer "how far past the 60-second SLO is this", and a rounded
 * relative phrase cannot answer that.
 */
export function formatAge(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  if (h > 0) return `${h}h ${pad(m)}m ${pad(s)}s`;
  if (m > 0) return `${m}m ${pad(s)}s`;
  return `${s}s`;
}

/** Gate 2's per-user bound: a pending revocation older than this is late. */
export const REVOCATION_SLO_SECONDS = 60;
