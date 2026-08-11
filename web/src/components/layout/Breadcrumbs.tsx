// SRS §8.2: "every view implements a dynamic breadcrumb derived from router
// state, not defined per page." No page component ever renders a
// `<Breadcrumbs items={...}/>` — this component alone walks `useMatches()`
// and reads each matched route's own `staticData.breadcrumbKey` (set once,
// in that route's file, e.g. `createFileRoute("/_authed/")({ staticData: {
// breadcrumbKey: "nav.dashboard" } })`). Add a route, get a crumb, for free.
//
// PHASE 17: a static i18n key can't name "Svea Jormungand" or "Hangar Corp"
// — a detail route's crumb is a value, not a label. Such a route's loader
// returns `{ breadcrumb: "<entity name>" }` (still router state: the
// router ran the loader and cached its result, same as everything else
// useMatches() reports) and this component prefers that verbatim string
// over resolving staticData.breadcrumbKey through t(). The page component
// still never touches breadcrumb rendering — it only supplies the value
// through the loader contract every route already has.
import { Link, useMatches } from "@tanstack/react-router";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight } from "lucide-react";

export function Breadcrumbs() {
  const matches = useMatches();
  const { t } = useTranslation();

  const crumbs = matches
    .map((m) => {
      const breadcrumbKey = (m.staticData as { breadcrumbKey?: string })
        .breadcrumbKey;
      const dynamicLabel = (m.loaderData as { breadcrumb?: string } | undefined)
        ?.breadcrumb;
      const label =
        dynamicLabel ?? (breadcrumbKey ? t(breadcrumbKey) : undefined);
      return { key: m.id, pathname: m.pathname, label };
    })
    .filter(
      (m): m is { key: string; pathname: string; label: string } =>
        typeof m.label === "string",
    );

  if (crumbs.length === 0) return null;

  return (
    <nav
      aria-label={t("breadcrumbs.home")}
      className="flex items-center gap-1 text-sm text-muted-foreground"
    >
      {crumbs.map((crumb, i) => {
        const isLast = i === crumbs.length - 1;
        return (
          <Fragment key={crumb.key}>
            {i > 0 && (
              <ChevronRight className="size-3.5 shrink-0" aria-hidden="true" />
            )}
            {isLast ? (
              <span className="font-medium text-foreground">{crumb.label}</span>
            ) : (
              <Link to={crumb.pathname} className="hover:text-foreground">
                {crumb.label}
              </Link>
            )}
          </Fragment>
        );
      })}
    </nav>
  );
}
