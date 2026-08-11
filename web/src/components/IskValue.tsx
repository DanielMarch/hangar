// Renders an ISK wire string (Principle 9 — money is a JSON string
// everywhere) with thousands separators, `font-mono`/`tabular-nums` for
// decimal alignment (SRS §8.1). Never `Number()`/`parseFloat()` on the raw
// value — web/eslint-rules/no-number-on-isk.js blocks that at lint time and
// web/src/lib/isk.ts does the formatting with string/BigInt math only.
//
// "ISK" itself is a currency code, not translated UI copy (EVE Online never
// localises it — legacy SeAT keeps it bare in every one of its 9 locales
// too), so it is not routed through i18next.
import { formatIsk, isValidIsk } from "@/lib/isk";
import { cn } from "@/lib/utils";

export function IskValue({
  isk,
  className,
}: {
  isk: string;
  className?: string;
}) {
  if (!isValidIsk(isk)) {
    return (
      <span
        className={cn(
          "font-mono tabular-nums text-muted-foreground",
          className,
        )}
      >
        —
      </span>
    );
  }
  return (
    <span className={cn("font-mono tabular-nums", className)}>
      {formatIsk(isk)}
      {" ISK"}
    </span>
  );
}
