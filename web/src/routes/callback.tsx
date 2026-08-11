// Defensive-only page. The real SSO callback, `GET /auth/callback`, is
// registered directly on the Go mux (internal/api/v1/auth.go,
// RegisterAuthRedirects) and is matched BEFORE the SPA's catch-all
// `http.FileServerFS` handler (cmd/hangar/serve.go) — EVE SSO's redirect
// never reaches the React router at all; the backend completes the login,
// re-issues the session cookie, and 302s straight to "/". This route (a
// distinct path, `/callback`, not `/auth/callback`) exists only so a
// mistyped or bookmarked URL doesn't hit a bare 404 — it just sends the
// visitor back through the real flow.
import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/callback")({
  component: () => <Navigate to="/login" />,
});
