import { createFileRoute } from "@tanstack/react-router";

import { CharacterPI } from "@/features/character/CharacterPI";

export const Route = createFileRoute("/_authed/characters/$characterId/pi")({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterPI characterId={Number(characterId)} />;
  },
});
