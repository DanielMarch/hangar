// Phase 0 shell only: proves the Vite/React/Tailwind build pipeline and the
// embed.FS pipeline into the Go binary work end to end. Routing (TanStack
// Router), data fetching (TanStack Query), and every real screen land in
// later phases per docs/03_IMPLEMENTATION_ROADMAP.md.
export function App() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background text-foreground">
      <p className="text-sm text-muted-foreground">HANGAR — bootstrap shell (Phase 0)</p>
    </main>
  );
}
