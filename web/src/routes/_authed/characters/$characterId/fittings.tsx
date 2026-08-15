import { createFileRoute } from "@tanstack/react-router";

import { CharacterFittings } from "@/features/character/CharacterFittings";

export const Route = createFileRoute(
  "/_authed/characters/$characterId/fittings",
)({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterFittings characterId={Number(characterId)} />;
  },
});
