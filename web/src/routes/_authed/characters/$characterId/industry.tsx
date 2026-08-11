import { createFileRoute } from "@tanstack/react-router";

import { CharacterIndustry } from "@/features/character/CharacterIndustry";

export const Route = createFileRoute(
  "/_authed/characters/$characterId/industry",
)({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterIndustry characterId={Number(characterId)} />;
  },
});
