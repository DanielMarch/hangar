// Custom local ESLint rule (Phase 16 exit criterion `TestESLintBlocksNumberOnISK`).
//
// Money is a JSON *string* on the wire everywhere (Principle 9). Passing a
// money-named value through `Number()` or `parseFloat()` silently
// reintroduces IEEE-754 float imprecision for large balances — exactly what
// the string wire format exists to prevent. web/src/lib/isk.ts is the
// sanctioned, string/BigInt-only formatter; this rule blocks the
// float-coercion escape hatch everywhere else.
//
// PHASE 17 WIDENING. Phase 16 matched only `/isk/i`, which caught
// `iskBalance` but not the wallet/ledger/contract fields Phase 17 actually
// renders — `amount`, `balance`, `tax`, `unit_price`, `total`, `escrow`,
// `collateral`, `buyout`, `reward`, `cost`, `payout` are all
// decimal.Decimal/domain.Money on the Go side (internal/domain/money.go's
// `moneyTokens`) and were never caught here. This is the same latent gap
// class as Phase 15.1's — a guard whose vocabulary silently drifted from
// the thing it guards. MONEY_TOKENS/NOT_MONEY below are a direct port of
// internal/domain/money.go's `moneyTokens`/`notMoneyFields`; keep them in
// sync by hand (there is no shared source file across the Go/TS boundary
// for a per-field vocabulary the way there is for internal/i18n/locales.json).
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

// Mirrors internal/domain/money.go's notMoneyFields — names that would
// otherwise false-positive against MONEY_TOKENS.
const NOT_MONEY = new Set([
  "tax_rate",
  "taxrate",
  "security_status",
  "securitystatus",
  "volume_remain",
  "volumeremain",
  "volume",
  "quantity",
  "runs",
]);

function splitWords(name) {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .split(/[_-]+/)
    .map((w) => w.toLowerCase())
    .filter(Boolean);
}

function isMoneyFieldName(name) {
  if (NOT_MONEY.has(name.toLowerCase())) return false;
  return splitWords(name).some((w) => MONEY_TOKENS.has(w));
}

function moneyIdentifierName(node) {
  if (!node) return null;
  if (node.type === "Identifier" && isMoneyFieldName(node.name))
    return node.name;
  if (
    node.type === "MemberExpression" &&
    !node.computed &&
    node.property.type === "Identifier" &&
    isMoneyFieldName(node.property.name)
  ) {
    return node.property.name;
  }
  return null;
}

/** @type {import("eslint").Rule.RuleModule} */
export default {
  meta: {
    type: "problem",
    docs: {
      description:
        "Disallow Number()/parseFloat() on a money-named value (Principle 9 vocabulary) — money is a wire string; use web/src/lib/isk.ts instead of float coercion.",
    },
    schema: [],
    messages: {
      noNumberOnIsk:
        "Do not call {{fn}}() on '{{name}}' — money is a JSON string on the wire (Principle 9). Use formatIsk()/isValidIsk() from web/src/lib/isk.ts instead of float coercion.",
    },
  },
  create(context) {
    return {
      CallExpression(node) {
        const callee = node.callee;
        let fnName = null;
        if (
          callee.type === "Identifier" &&
          (callee.name === "Number" || callee.name === "parseFloat")
        ) {
          fnName = callee.name;
        } else if (
          callee.type === "MemberExpression" &&
          !callee.computed &&
          callee.object.type === "Identifier" &&
          callee.object.name === "Number" &&
          callee.property.type === "Identifier" &&
          callee.property.name === "parseFloat"
        ) {
          fnName = "Number.parseFloat";
        }
        if (!fnName) return;

        const name = moneyIdentifierName(node.arguments[0]);
        if (name) {
          context.report({
            node,
            messageId: "noNumberOnIsk",
            data: { fn: fnName, name },
          });
        }
      },
    };
  },
};
