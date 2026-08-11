// Client-only state around the auth *flow*, never the session itself: who
// the caller is comes from TanStack Query's `useMe()` (server state, cached
// from GET /api/v1/me) and MUST NOT be duplicated here (SRS §8.3). What
// belongs here is UI-only and cannot be derived from the server response:
// the user's locale preference (nothing server-side depends on it beyond
// the ESI Accept-Language header the gateway derives per-request) and a
// one-shot flag so the login screen can say "you were logged out" after a
// client-initiated POST /auth/logout, which — unlike /auth/login and
// /auth/callback — IS a same-origin fetch this SPA makes itself.
import { create } from "zustand";
import { persist } from "zustand/middleware";

import { uiLocales } from "@/lib/locales";

interface SessionUiState {
  locale: string;
  setLocale: (locale: string) => void;
  justLoggedOut: boolean;
  markLoggedOut: () => void;
  clearLoggedOutFlag: () => void;
}

const defaultLocale = uiLocales()[0] ?? "en";

export const useSessionUiStore = create<SessionUiState>()(
  persist(
    (set) => ({
      locale: defaultLocale,
      setLocale: (locale) => set({ locale }),
      justLoggedOut: false,
      markLoggedOut: () => set({ justLoggedOut: true }),
      clearLoggedOutFlag: () => set({ justLoggedOut: false }),
    }),
    { name: "hangar.session-ui", partialize: (s) => ({ locale: s.locale }) },
  ),
);
