import { createFileRoute } from "@tanstack/react-router";

import { CharacterAssets } from "@/features/character/CharacterAssets";

export const Route = createFileRoute("/_authed/characters/$characterId/assets")(
  {
    component: () => {
      const { characterId } = Route.useParams();
      return <CharacterAssets characterId={Number(characterId)} />;
    },
  },
);
