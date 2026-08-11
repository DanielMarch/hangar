import { createFileRoute } from "@tanstack/react-router";

import { CharacterIntel } from "@/features/character/CharacterIntel";

export const Route = createFileRoute("/_authed/characters/$characterId/intel")({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterIntel characterId={Number(characterId)} />;
  },
});
