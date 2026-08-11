import { createFileRoute } from "@tanstack/react-router";

import { CharacterContracts } from "@/features/character/CharacterContracts";

export const Route = createFileRoute(
  "/_authed/characters/$characterId/contracts",
)({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterContracts characterId={Number(characterId)} />;
  },
});
