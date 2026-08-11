// SSO login screen. The actual login flow is a full-page navigation to
// `GET /auth/login` (internal/api/v1/auth.go, RegisterAuthRedirects) — that
// handler 302s the browser to EVE SSO and sets the pre-auth `hangar_session`
// cookie itself, so this is a plain `<a href>`, never a client-side
// fetch/Link: an OAuth authorization redirect cannot be an XHR.
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { meQueryOptions } from "@/api/queries/me";

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
  beforeLoad: async ({ context }) => {
    // Already signed in (e.g. a stale /login bookmark, or the back button
    // after a successful login) — don't show the login screen again.
    const cached = context.queryClient.getQueryData(meQueryOptions.queryKey);
    if (cached) {
      throw redirect({ to: "/" });
    }
  },
  component: LoginPage,
});

function LoginPage() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-6 bg-background p-6 text-foreground">
      <div className="flex flex-col items-center gap-2 text-center">
        <span className="font-mono text-lg font-semibold tracking-wide">
          {t("app.name")}
        </span>
        <h1 className="text-2xl font-semibold">{t("login.heading")}</h1>
        <p className="max-w-sm text-sm text-muted-foreground">
          {t("login.subheading")}
        </p>
      </div>
      <a
        href="/auth/login"
        className="inline-flex h-10 items-center justify-center rounded-md bg-primary px-6 text-sm font-medium text-primary-foreground hover:bg-primary/90"
      >
        {t("login.cta")}
      </a>
    </div>
  );
}
