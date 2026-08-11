import { createFileRoute } from "@tanstack/react-router";

import { CharacterSkills } from "@/features/character/CharacterSkills";

export const Route = createFileRoute("/_authed/characters/$characterId/skills")(
  {
    component: () => {
      const { characterId } = Route.useParams();
      return <CharacterSkills characterId={Number(characterId)} />;
    },
  },
);
