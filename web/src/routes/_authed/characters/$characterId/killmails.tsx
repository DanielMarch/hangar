import { createFileRoute } from "@tanstack/react-router";

import { CharacterKillmails } from "@/features/character/CharacterKillmails";

export const Route = createFileRoute(
  "/_authed/characters/$characterId/killmails",
)({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterKillmails characterId={Number(characterId)} />;
  },
});
