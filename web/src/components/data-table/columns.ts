// Column builder helpers for DataTable.tsx. SRS §8.1: "All data tables, ISK
// values and identifiers must use font-mono or tabular-nums." Every builder
// below that touches a numeric/identifier/money value applies one of those
// classes so no feature screen has to remember to.
//
// `autoColumns` is the fallback used by the long tail of Phase 17 list
// screens that don't warrant a hand-picked column set (72 legacy
// controllers; only the marquee surfaces — wallet, skills, assets,
// contracts, mail, killmails, members — get explicit `columns` arrays). It
// infers a reasonable column from each key in a sample row using the same
// money vocabulary internal/domain/money.go and
// web/eslint-rules/no-number-on-isk.js use, so an auto-rendered `amount`
// column is formatted as ISK without any screen having to say so.
import type { ColumnDef } from "@tanstack/react-table";

import { formatIsk, isValidIsk } from "@/lib/isk";

export type Row = Record<string, unknown>;

const MONEY_TOKENS = new Set([
  "isk",
  "amount",
  "balance",
  "price",
  "total",
  "tax",
  "reward",
  "collateral",
  "buyout",
  "escrow",
  "cost",
  "payout",
]);
const NOT_MONEY = new Set([
  "tax_rate",
  "security_status",
  "volume_remain",
  "volume",
  "quantity",
  "runs",
]);

function isMoneyKey(key: string): boolean {
  const lower = key.toLowerCase();
  if (NOT_MONEY.has(lower)) return false;
  return lower.split("_").some((w) => MONEY_TOKENS.has(w));
}

function isIdKey(key: string): boolean {
  return /(^|_)id$/.test(key) || key === "id";
}

function isDateKey(key: string): boolean {
  return /_at$/.test(key) || key === "date";
}

export function textColumn(key: string, header: string): ColumnDef<Row> {
  return {
    id: key,
    accessorKey: key,
    header,
    cell: ({ getValue }) => {
      const v = getValue();
      return v === null || v === undefined || v === "" ? "—" : String(v);
    },
  };
}

export function idColumn(key: string, header: string): ColumnDef<Row> {
  return {
    id: key,
    accessorKey: key,
    header,
    meta: { className: "font-mono tabular-nums" },
    cell: ({ getValue }) => String(getValue() ?? "—"),
  };
}

export function numberColumn(key: string, header: string): ColumnDef<Row> {
  return {
    id: key,
    accessorKey: key,
    header,
    meta: { className: "font-mono tabular-nums text-right" },
    cell: ({ getValue }) => {
      const v = getValue();
      return v === null || v === undefined ? "—" : String(v);
    },
  };
}

/** Renders an ISK/money wire string via web/src/lib/isk.ts — never
 * Number()/parseFloat() (Principle 9). */
export function iskColumn(key: string, header: string): ColumnDef<Row> {
  return {
    id: key,
    accessorKey: key,
    header,
    meta: { className: "font-mono tabular-nums text-right" },
    cell: ({ getValue }) => {
      const v = getValue();
      if (typeof v !== "string" || !isValidIsk(v)) return "—";
      return formatIsk(v);
    },
  };
}

export function dateColumn(key: string, header: string): ColumnDef<Row> {
  return {
    id: key,
    accessorKey: key,
    header,
    meta: { className: "font-mono tabular-nums" },
    cell: ({ getValue }) => {
      const v = getValue();
      if (typeof v !== "string") return "—";
      const d = new Date(v);
      return Number.isNaN(d.getTime()) ? v : d.toLocaleString();
    },
  };
}

export function boolColumn(key: string, header: string): ColumnDef<Row> {
  return {
    id: key,
    accessorKey: key,
    header,
    cell: ({ getValue }) => (getValue() ? "Yes" : "No"),
  };
}

/**
 * Infers a column set from one sample row's keys. See file banner.
 *
 * Headers here are the raw API field name (underscores turned to spaces),
 * NOT routed through i18next: they are data-shape labels for the long
 * tail of screens that don't warrant a hand-picked, translated column set
 * (comparable to displaying a raw JSON key), not authored UI copy. Every
 * screen with a real column array (skills, assets, wallet, contracts,
 * mail, ...) builds it with `t("columns.*")` inside the component instead
 * of calling this.
 */
export function autoColumns(
  sample: Row,
  opts: { exclude?: string[]; limit?: number } = {},
): ColumnDef<Row>[] {
  const exclude = new Set(opts.exclude ?? []);
  const keys = Object.keys(sample)
    .filter((k) => !exclude.has(k) && !k.startsWith("$"))
    .slice(0, opts.limit ?? 12);

  return keys.map((key) => {
    const header = key.replace(/_/g, " ");
    const value = sample[key];
    if (isMoneyKey(key)) return iskColumn(key, header);
    if (isDateKey(key)) return dateColumn(key, header);
    if (isIdKey(key)) return idColumn(key, header);
    if (typeof value === "boolean") return boolColumn(key, header);
    if (typeof value === "number") return numberColumn(key, header);
    return textColumn(key, header);
  });
}
