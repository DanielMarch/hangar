import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { ApiError } from "./api/client";
import "./i18n";
import "./styles/index.css";
import { routeTree } from "./routeTree.gen";

// PHASE 20.2, DEFECT B40 (the client half).
//
// This was a flat `retry: 1`, so every 401 and every 403 was issued twice.
// A permission failure is definitionally not transient — the server's
// answer to "may this user do this" does not change between two requests
// 300ms apart — so the retry bought nothing and cost double the requests,
// double the log lines, and a visibly slower failure on precisely the
// screens a narrowly-permissioned user hits most. `meQueryOptions` already
// set `retry: false` locally for exactly this reason; this makes the same
// judgement the default instead of a per-query workaround.
//
// A 5xx or a transport failure still gets its one retry: those genuinely
// can be transient, which is what the retry was for.
function retryOnlyTransient(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
    return false;
  }
  return failureCount < 1;
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Server data is fetched behind a resolved session cookie; a stale
      // read is far cheaper than hammering the API on every mount.
      staleTime: 30_000,
      retry: retryOnlyTransient,
    },
  },
});

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const container = document.getElementById("root");
if (!container) {
  throw new Error("main.tsx: #root element not found");
}

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
