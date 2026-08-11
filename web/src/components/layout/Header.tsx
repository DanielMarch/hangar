// Top header: contextual breadcrumbs + session controls (SRS §8.2).
import { useNavigate } from "@tanstack/react-router";
import { LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useMe } from "@/api/queries/me";
import { useSessionUiStore } from "@/stores/session";

function characterPortraitUrl(characterId: unknown): string | undefined {
  if (typeof characterId !== "number") return undefined;
  return `https://images.evetech.net/characters/${characterId}/portrait?size=64`;
}

export function Header() {
  const { t } = useTranslation();
  const { data: me } = useMe();
  const navigate = useNavigate();
  const markLoggedOut = useSessionUiStore((s) => s.markLoggedOut);

  const displayName =
    typeof me?.display_name === "string" ? me.display_name : undefined;
  const portraitUrl = characterPortraitUrl(me?.main_character_id);

  async function handleLogout() {
    await fetch("/auth/logout", { method: "POST", credentials: "include" });
    markLoggedOut();
    await navigate({ to: "/login" });
  }

  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-background px-4">
      <Breadcrumbs />

      {displayName && (
        <DropdownMenu>
          <DropdownMenuTrigger className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent">
            <Avatar>
              {portraitUrl && <AvatarImage src={portraitUrl} alt="" />}
              <AvatarFallback>
                {displayName.slice(0, 2).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <span className="font-mono">{displayName}</span>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem variant="destructive" onSelect={handleLogout}>
              <LogOut className="size-4" aria-hidden="true" />
              {t("actions.logout")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </header>
  );
}
