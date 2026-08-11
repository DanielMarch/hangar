// ISK is a JSON *string* on the wire everywhere (Principle 9 — SRS §5,
// 01_ARCHITECTURE.md). ESI/legacy SeAT wallet balances can carry two decimal
// places and characters/corporations can hold more ISK than
// Number.MAX_SAFE_INTEGER cleanly represents, so this module formats ISK for
// display via string/BigInt manipulation ONLY — never `Number()` or
// `parseFloat()`. web/eslint-rules/no-number-on-isk.js is the lint gate that
// keeps every other call site honest; this file is the one place that is
// allowed to reason about the digits, and even it never floats them.
const ISK_STRING = /^-?\d+(\.\d+)?$/;

export class InvalidIskValueError extends Error {
  constructor(raw: string) {
    super(
      `isk: "${raw}" is not a valid ISK wire value (expected an optionally-signed decimal string)`,
    );
    this.name = "InvalidIskValueError";
  }
}

/**
 * Formats an ISK wire string with thousands separators, preserving exact
 * digits (no float round-trip). `fractionDigits` truncates (never rounds via
 * float math) the decimal part for display; the underlying value is never
 * touched.
 */
export function formatIsk(
  iskString: string,
  opts: { fractionDigits?: number } = {},
): string {
  if (!ISK_STRING.test(iskString)) {
    throw new InvalidIskValueError(iskString);
  }
  const negative = iskString.startsWith("-");
  const unsigned = negative ? iskString.slice(1) : iskString;
  const [wholePart, decimalPart = ""] = unsigned.split(".");

  const grouped = wholePart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");

  const fractionDigits = opts.fractionDigits ?? 2;
  const decimal = decimalPart
    .padEnd(fractionDigits, "0")
    .slice(0, fractionDigits);

  const sign =
    negative && (wholePart !== "0" || /[1-9]/.test(decimal)) ? "-" : "";
  return fractionDigits > 0
    ? `${sign}${grouped}.${decimal}`
    : `${sign}${grouped}`;
}

/** True if `raw` is a well-formed ISK wire string. */
export function isValidIsk(raw: string): boolean {
  return ISK_STRING.test(raw);
}
