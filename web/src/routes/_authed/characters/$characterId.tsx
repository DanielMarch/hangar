// Layout route for one character: tab nav + <Outlet/> for the sheet/
// skills/assets/wallet/... child routes. `loader` resolves the character's
// display name into `breadcrumb` — Breadcrumbs.tsx prefers that dynamic
// value over a static i18n key (Phase 17 addition to the Phase 16 pattern:
// see that component's file banner). Every character sub-resource shares
// the single "characters.view" RBAC permission (SRS §5.2), so a 403 will
// surface identically on whichever tab is active — each tab's own
// ItemPanel/CollectionTable renders that Forbidden state itself, so this
// layout doesn't need to special-case it.
import {
  createFileRoute,
  Link,
  Outlet,
  useParams,
} from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { characterQueryOptions } from "@/features/character/queries";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authed/characters/$characterId")({
  loader: async ({ params, context }) => {
    try {
      const result = await context.queryClient.ensureQueryData(
        characterQueryOptions(Number(params.characterId)),
      );
      const name =
        typeof result.data?.name === "string"
          ? result.data.name
          : params.characterId;
      return { breadcrumb: name };
    } catch {
      return { breadcrumb: params.characterId };
    }
  },
  component: CharacterLayout,
});

const TABS = [
  {
    to: "/characters/$characterId",
    labelKey: "characters.tabs.sheet",
    exact: true,
  },
  { to: "/characters/$characterId/skills", labelKey: "characters.tabs.skills" },
  { to: "/characters/$characterId/assets", labelKey: "characters.tabs.assets" },
  { to: "/characters/$characterId/wallet", labelKey: "characters.tabs.wallet" },
  {
    to: "/characters/$characterId/contracts",
    labelKey: "characters.tabs.contracts",
  },
  { to: "/characters/$characterId/mail", labelKey: "characters.tabs.mail" },
  { to: "/characters/$characterId/pi", labelKey: "characters.tabs.pi" },
  {
    to: "/characters/$characterId/calendar",
    labelKey: "characters.tabs.calendar",
  },
  {
    to: "/characters/$characterId/industry",
    labelKey: "characters.tabs.industry",
  },
  {
    to: "/characters/$characterId/killmails",
    labelKey: "characters.tabs.killmails",
  },
  { to: "/characters/$characterId/intel", labelKey: "characters.tabs.intel" },
] as const;

function CharacterLayout() {
  const { t } = useTranslation();
  const { characterId } = useParams({
    from: "/_authed/characters/$characterId",
  });

  return (
    <div className="space-y-4">
      <nav className="flex flex-wrap gap-1 border-b border-border pb-2">
        {TABS.map((tab) => (
          <Link
            key={tab.to}
            to={tab.to}
            params={{ characterId }}
            activeOptions={{ exact: "exact" in tab && tab.exact }}
            className="rounded-md px-2.5 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            activeProps={{ className: cn("bg-accent text-cyan-400") }}
          >
            {t(tab.labelKey)}
          </Link>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
