import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ConnectionStatus, Marble, Organization, Tokens, User } from "@/lib/types";
import { clearTokens, loadTokens, saveTokens } from "@/lib/api";

/**
 * Zustand holds only UI/session state. All server state lives in TanStack
 * Query — the socket surgically patches that cache rather than duplicating it.
 */

interface AuthSlice {
  user: User | null;
  organization: Organization | null;
  isAuthenticated: boolean;
  setSession: (user: User, organization: Organization | null, tokens: Tokens) => void;
  setIdentity: (user: User | null, organization: Organization | null) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthSlice>()(
  persist(
    (set) => ({
      user: null,
      organization: null,
      isAuthenticated: Boolean(loadTokens()?.access_token),

      setSession: (user, organization, tokens) => {
        saveTokens(tokens);
        set({ user, organization, isAuthenticated: true });
      },

      setIdentity: (user, organization) => set({ user, organization }),

      logout: () => {
        clearTokens();
        set({ user: null, organization: null, isAuthenticated: false });
      },
    }),
    {
      name: "marblejar.session",
      partialize: (s) => ({ user: s.user, organization: s.organization }),
      onRehydrateStorage: () => (state) => {
        // A persisted profile without a live token is not an active session.
        if (state) state.isAuthenticated = Boolean(loadTokens()?.access_token);
      },
    },
  ),
);

export interface QueueFilters {
  project: string;
  model: string;
  agentId: string;
  status: string;
  q: string;
  range: "1h" | "24h" | "7d" | "30d" | "all";
}

export const emptyFilters: QueueFilters = {
  project: "",
  model: "",
  agentId: "",
  status: "",
  q: "",
  range: "24h",
};

interface UiSlice {
  filters: QueueFilters;
  setFilter: <K extends keyof QueueFilters>(key: K, value: QueueFilters[K]) => void;
  resetFilters: () => void;

  wsStatus: ConnectionStatus;
  setWsStatus: (s: ConnectionStatus) => void;

  activeMarbleId: string | null;
  openMarble: (id: string | null) => void;

  /** Marbles queued for the jar drop animation, drained by JarCanvas. */
  jarQueue: Marble[];
  enqueueMarble: (m: Marble) => void;
  dequeueMarble: () => Marble | undefined;

  /** Marble currently being dragged onto an objective card. */
  dragMarbleId: string | null;
  setDragMarble: (id: string | null) => void;

  theme: "dark" | "light";
  toggleTheme: () => void;
}

export const useUiStore = create<UiSlice>()(
  persist(
    (set, get) => ({
      filters: emptyFilters,
      setFilter: (key, value) => set({ filters: { ...get().filters, [key]: value } }),
      resetFilters: () => set({ filters: emptyFilters }),

      wsStatus: "connecting",
      setWsStatus: (wsStatus) => set({ wsStatus }),

      activeMarbleId: null,
      openMarble: (activeMarbleId) => set({ activeMarbleId }),

      jarQueue: [],
      enqueueMarble: (m) => {
        // Defensive dedup: the same marble must never be queued for the jar
        // animation twice (e.g. if a duplicate socket event arrives).
        const { jarQueue } = get();
        if (jarQueue.some((q) => q.id === m.id)) return;
        // Bound the queue so a burst of agent activity cannot grow it forever.
        const next = [...jarQueue, m];
        set({ jarQueue: next.length > 50 ? next.slice(-50) : next });
      },
      dequeueMarble: () => {
        const [head, ...rest] = get().jarQueue;
        if (!head) return undefined;
        set({ jarQueue: rest });
        return head;
      },

      dragMarbleId: null,
      setDragMarble: (dragMarbleId) => set({ dragMarbleId }),

      theme: "dark",
      toggleTheme: () => {
        const theme = get().theme === "dark" ? "light" : "dark";
        document.documentElement.classList.toggle("dark", theme === "dark");
        set({ theme });
      },
    }),
    {
      name: "marblejar.ui",
      partialize: (s) => ({ theme: s.theme, filters: s.filters }),
      onRehydrateStorage: () => (state) => {
        if (state) document.documentElement.classList.toggle("dark", state.theme === "dark");
      },
    },
  ),
);
