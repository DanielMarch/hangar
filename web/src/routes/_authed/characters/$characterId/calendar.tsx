import { createFileRoute } from "@tanstack/react-router";

import { CharacterCalendar } from "@/features/character/CharacterCalendar";

export const Route = createFileRoute(
  "/_authed/characters/$characterId/calendar",
)({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterCalendar characterId={Number(characterId)} />;
  },
});
