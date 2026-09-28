"use client";

import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Session = {
  userId: string;
  username: string;
  accessToken: string;
  refreshToken: string;
};

type AuthState = {
  session: Session | null;
  setSession: (session: Session) => void;
  clear: () => void;
};

export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      session: null,
      setSession: (session) => set({ session }),
      clear: () => set({ session: null }),
    }),
    { name: "rokko-session" },
  ),
);

export function sessionFromAuth(res: { userId: string; username: string; accessToken: string; refreshToken: string }): Session {
  return {
    userId: res.userId,
    username: res.username,
    accessToken: res.accessToken,
    refreshToken: res.refreshToken,
  };
}
