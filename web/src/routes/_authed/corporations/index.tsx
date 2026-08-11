// No bare GET /api/v1/corporations list route exists (same shape as
// characters — SRS §6.7 backs this with /support/search instead), so this
// is search-only, plus a shortcut into "my corporation" derived from the
// caller's own linked characters.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useMyCharacters } from "@/api/queries/me";
import { useEntitySearch } from "@/api/queries/search";
import { Button } from "@/components/ui/button";

export const Route = createFileRoute("/_authed/corporations/")({
  staticData: { breadcrumbKey: "nav.corporations" },
  component: CorporationsIndex,
});

function CorporationsIndex() {
  const { t } = useTranslation();
  const mine = useMyCharacters();
  const search = useEntitySearch();
  const [query, setQuery] = useState("");

  const myCorporationIds = Array.from(
    new Set(
      (mine.data?.rows ?? [])
        .map((c) => c.corporation_id)
        .filter((id): id is number => typeof id === "number"),
    ),
  );

  return (
    <div className="space-y-8">
      <div>
        <h1 className="mb-4 text-xl font-semibold">
          {t("corporations.heading")}
        </h1>
        {myCorporationIds.length > 0 && (
          <ul className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {myCorporationIds.map((id) => (
              <li key={id}>
                <Link
                  to="/corporations/$corporationId"
                  params={{ corporationId: String(id) }}
                  className="block rounded-md border border-border bg-card p-3 font-mono text-sm hover:border-cyan-500"
                >
                  {id}
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
            if (query.trim()) search.mutate({ query, kinds: ["corporation"] });
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
              .filter((r) => r.kind === "corporation")
              .map((r) => (
                <li key={r.id}>
                  <Link
                    to="/corporations/$corporationId"
                    params={{ corporationId: String(r.id) }}
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
