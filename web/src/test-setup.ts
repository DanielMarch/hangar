// Vitest setup (jsdom environment). jsdom doesn't implement scrollTo/scrollIntoView;
// TanStack Router's scroll restoration calls window.scrollTo on every
// navigation, which would otherwise spam every route/component test with a
// harmless "Not implemented" error.
if (typeof window !== "undefined") {
  window.scrollTo = () => {};
}

// jsdom performs no real layout, so every element's clientHeight/
// offsetHeight is 0 by default. @tanstack/react-virtual (DataTable.tsx)
// would then — correctly, for a genuinely 0px viewport — render zero rows,
// which is right for jsdom's real behavior but wrong for testing anything
// that renders a DataTable. Give every element a plausible viewport height
// globally so virtualized tables behave under test the way they would in
// a real browser.
Object.defineProperty(HTMLElement.prototype, "clientHeight", {
  configurable: true,
  value: 600,
});
Object.defineProperty(HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  value: 600,
});
