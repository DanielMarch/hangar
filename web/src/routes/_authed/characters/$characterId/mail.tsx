import { createFileRoute } from "@tanstack/react-router";

import { CharacterMail } from "@/features/character/CharacterMail";

export const Route = createFileRoute("/_authed/characters/$characterId/mail")({
  component: () => {
    const { characterId } = Route.useParams();
    return <CharacterMail characterId={Number(characterId)} />;
  },
});
