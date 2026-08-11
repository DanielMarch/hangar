import { createFileRoute } from "@tanstack/react-router";

import { CharacterWallet } from "@/features/character/CharacterWallet";

export const Route = createFileRoute("/_authed/characters/$characterId/wallet")(
  {
    component: () => {
      const { characterId } = Route.useParams();
      return <CharacterWallet characterId={Number(characterId)} />;
    },
  },
);
