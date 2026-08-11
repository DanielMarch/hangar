import { createFileRoute } from "@tanstack/react-router";

import { CharacterSheet } from "@/features/character/CharacterSheet";

export const Route = createFileRoute("/_authed/characters/$characterId/")({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterSheet characterId={Number(characterId)} />;
  },
});
