// Vitest setup (jsdom environment). jsdom doesn't implement scrollTo/scrollIntoView;
// TanStack Router's scroll restoration calls window.scrollTo on every
// navigation, which would otherwise spam every route/component test with a
// harmless "Not implemented" error.
if (typeof window !== "undefined") {
  window.scrollTo = () => {};
}
