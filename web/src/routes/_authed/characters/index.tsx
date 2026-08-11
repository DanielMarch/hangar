import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useMyCharacters } from "@/api/queries/me";
import { useEntitySearch } from "@/api/queries/search";
import { Button } from "@/components/ui/button";
import { CardSkeleton } from "@/components/Skeleton";
import { Forbidden, isForbidden } from "@/components/PermissionGate";

export const Route = createFileRoute("/_authed/characters/")({
  staticData: { breadcrumbKey: "nav.characters" },
  component: CharactersIndex,
});

function CharactersIndex() {
  const { t } = useTranslation();
  const mine = useMyCharacters();
  const search = useEntitySearch();
  const [query, setQuery] = useState("");

  return (
    <div className="space-y-8">
      <div>
        <h1 className="mb-4 text-xl font-semibold">
          {t("characters.heading")}
        </h1>
        {mine.isPending ? (
          <CardSkeleton />
        ) : mine.error ? (
          isForbidden(mine.error) ? (
            <Forbidden detail={mine.error.detail} />
          ) : null
        ) : (
          <ul className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {mine.data.rows.map((c) => (
              <li key={String(c.character_id)}>
                <Link
                  to="/characters/$characterId"
                  params={{ characterId: String(c.character_id) }}
                  className="block rounded-md border border-border bg-card p-3 font-mono text-sm hover:border-cyan-500"
                >
                  {String(c.name ?? c.character_id)}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="max-w-md space-y-2">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (query.trim()) search.mutate({ query, kinds: ["character"] });
          }}
          className="flex gap-2"
        >
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("dataTable.filterPlaceholder")}
            className="h-9 flex-1 rounded-md border border-border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
          />
          <Button type="submit" size="sm">
            {t("actions.search")}
          </Button>
        </form>
        {search.data && (
          <ul className="space-y-1">
            {search.data
              .filter((r) => r.kind === "character")
              .map((r) => (
                <li key={r.id}>
                  <Link
                    to="/characters/$characterId"
                    params={{ characterId: String(r.id) }}
                    className="text-sm text-cyan-400 hover:underline"
                  >
                    {r.name}
                  </Link>
                </li>
              ))}
          </ul>
        )}
      </div>
    </div>
  );
}
