// Persistent collapsible left sidebar (SRS §8.2). Collapse state is
// client-only UI chrome, owned by Zustand (web/src/stores/ui.ts) — never
// server data. Nav entries beyond Dashboard are placeholders until their
// routes land (characters/corporations -> Phase 17, admin -> Phase 18); they
// render disabled rather than linking to routes that do not exist yet.
import { Link, useMatches } from "@tanstack/react-router";
import {
  Building2,
  LayoutDashboard,
  PanelLeftClose,
  PanelLeftOpen,
  Shield,
  Users,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { useUiStore } from "@/stores/ui";

const NAV_ITEMS = [
  {
    labelKey: "nav.dashboard",
    icon: LayoutDashboard,
    to: "/" as const,
    enabled: true,
  },
  { labelKey: "nav.characters", icon: Users, enabled: false },
  { labelKey: "nav.corporations", icon: Building2, enabled: false },
  { labelKey: "nav.squads", icon: Shield, enabled: false },
];

export function Sidebar() {
  const { t } = useTranslation();
  const collapsed = useUiStore((s) => s.sidebarCollapsed);
  const toggleSidebar = useUiStore((s) => s.toggleSidebar);
  const matches = useMatches();
  const currentPath = matches[matches.length - 1]?.pathname;

  return (
    <aside
      className={cn(
        "flex h-screen shrink-0 flex-col border-r border-border bg-card transition-[width] duration-150",
        collapsed ? "w-14" : "w-56",
      )}
    >
      <div
        className={cn(
          "flex h-14 items-center border-b border-border px-3",
          collapsed ? "justify-center" : "justify-between",
        )}
      >
        {!collapsed && (
          <span className="font-mono text-sm font-semibold tracking-wide">
            {t("app.name")}
          </span>
        )}
        <Button
          variant="ghost"
          size="icon"
          aria-label={
            collapsed
              ? t("actions.expandSidebar")
              : t("actions.collapseSidebar")
          }
          onClick={toggleSidebar}
        >
          {collapsed ? (
            <PanelLeftOpen className="size-4" />
          ) : (
            <PanelLeftClose className="size-4" />
          )}
        </Button>
      </div>

      <nav className="flex flex-1 flex-col gap-1 p-2">
        {NAV_ITEMS.map((item) => {
          const Icon = item.icon;
          const active = item.enabled && item.to === currentPath;
          const content = (
            <span
              className={cn(
                "flex items-center gap-2 rounded-md px-2 py-2 text-sm",
                item.enabled
                  ? "cursor-pointer hover:bg-accent hover:text-accent-foreground"
                  : "cursor-not-allowed opacity-40",
                active && "bg-accent text-cyan-400",
                collapsed && "justify-center",
              )}
            >
              <Icon className="size-4 shrink-0" aria-hidden="true" />
              {!collapsed && <span>{t(item.labelKey)}</span>}
            </span>
          );

          const wrapped =
            item.enabled && item.to ? (
              <Link key={item.labelKey} to={item.to}>
                {content}
              </Link>
            ) : (
              <div key={item.labelKey}>{content}</div>
            );

          if (!collapsed) return wrapped;
          return (
            <Tooltip key={item.labelKey}>
              <TooltipTrigger asChild>{wrapped}</TooltipTrigger>
              <TooltipContent side="right">{t(item.labelKey)}</TooltipContent>
            </Tooltip>
          );
        })}
      </nav>
    </aside>
  );
}
