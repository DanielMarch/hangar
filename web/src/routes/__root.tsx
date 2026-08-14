// Router root. Holds the QueryClient in router context (so route
// `beforeLoad` guards — see _authed.tsx — can call
// `context.queryClient.ensureQueryData(...)` without a second provider) and
// the last-resort error and not-found renderers: all real chrome lives in
// _authed.tsx's AppShell so the unauthenticated /login route isn't forced
// to render a sidebar.
import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  type ErrorComponentProps,
  Link,
  Outlet,
} from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

interface RouterContext {
  queryClient: QueryClient;
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootComponent,
  errorComponent: RootErrorComponent,
  notFoundComponent: RootNotFound,
});

function RootComponent() {
  return <Outlet />;
}

// ── PHASE 20.2, DEFECT B39 ───────────────────────────────────────────────
//
// The reported symptom was an unauthenticated GET / rendering a completely
// blank page. It did not reproduce as reported and is closed as such, but
// the investigation surfaced something that is a defect regardless of what
// caused the original observation: THIS ROUTE DECLARED NO errorComponent.
//
// TanStack Router treats a `redirect` thrown from `beforeLoad` as control
// flow and anything else as an error, and an error with no errorComponent
// anywhere up the tree renders NOTHING — no message, nothing a user would
// ever see, no way to tell a crash from a slow load. So any throw on the
// unauthenticated path produced exactly the blank page that was reported,
// whatever threw. That is what made this whole class invisible, and it is
// fixed here whether or not it was the original cause.
//
// ── WHY NO DESIGN-SYSTEM COMPONENTS ──────────────────────────────────────
// This is the last thing standing between a failure and a blank screen, so
// it uses plain elements and inline styles: it renders OUTSIDE AppShell
// (which is where the layout, theme and data-module error boundaries all
// live) and may be rendering precisely because something in that layer
// threw. Copy still goes through i18next — that initialises at module load
// in main.tsx, before React mounts at all, so if it were broken this
// component would never run either.
function RootErrorComponent({ error }: ErrorComponentProps) {
  const { t } = useTranslation();
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div role="alert" style={pageStyle}>
      <h1 style={headingStyle}>{t("errors.rootTitle")}</h1>
      <p style={{ marginBottom: "1rem" }}>{t("errors.rootBody")}</p>
      <pre
        style={{
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
          padding: "0.75rem",
          border: "1px solid rgba(127,127,127,0.4)",
          borderRadius: "0.375rem",
          fontSize: "0.8125rem",
        }}
      >
        {message}
      </pre>
      <p style={{ marginTop: "1rem" }}>
        <a href="/login">{t("errors.rootSignIn")}</a>
        {" · "}
        <a href="/">{t("errors.rootReload")}</a>
      </p>
    </div>
  );
}

// RootNotFound answers a client-side route that matches nothing. The server
// already serves index.html for any extensionless path (cmd/hangar's
// spaHandler), so an unknown deep link reaches the router rather than a
// 404 — and without this it would render the same blank page B39 is about.
function RootNotFound() {
  const { t } = useTranslation();
  return (
    <div style={pageStyle}>
      <h1 style={headingStyle}>{t("errors.notFoundTitle")}</h1>
      <p>
        <Link to="/">{t("errors.notFoundBack")}</Link>
      </p>
    </div>
  );
}

const pageStyle: React.CSSProperties = {
  maxWidth: "40rem",
  margin: "4rem auto",
  padding: "1.5rem",
  fontFamily: "system-ui, sans-serif",
  lineHeight: 1.5,
};

const headingStyle: React.CSSProperties = {
  fontSize: "1.25rem",
  fontWeight: 600,
  marginBottom: "0.5rem",
};
