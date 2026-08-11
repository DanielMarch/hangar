// Custom local ESLint rule (Phase 16 exit criterion `TestESLintBlocksNumberOnISK`).
//
// Money is a JSON *string* on the wire everywhere (Principle 9). Passing an
// ISK-named value through `Number()` or `parseFloat()` silently reintroduces
// IEEE-754 float imprecision for large balances — exactly what the string
// wire format exists to prevent. web/src/lib/isk.ts is the sanctioned,
// string/BigInt-only formatter; this rule blocks the float-coercion escape
// hatch everywhere else.
const ISK_NAME = /isk/i;

function iskIdentifierName(node) {
  if (!node) return null;
  if (node.type === "Identifier" && ISK_NAME.test(node.name)) return node.name;
  if (
    node.type === "MemberExpression" &&
    !node.computed &&
    node.property.type === "Identifier" &&
    ISK_NAME.test(node.property.name)
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
        "Disallow Number()/parseFloat() on an ISK-named value — ISK is a wire string; use web/src/lib/isk.ts instead of float coercion.",
    },
    schema: [],
    messages: {
      noNumberOnIsk:
        "Do not call {{fn}}() on '{{name}}' — ISK is a JSON string on the wire (Principle 9). Use formatIsk()/isValidIsk() from web/src/lib/isk.ts instead of float coercion.",
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

        const name = iskIdentifierName(node.arguments[0]);
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
