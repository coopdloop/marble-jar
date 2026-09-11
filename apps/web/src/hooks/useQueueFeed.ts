import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { API_BASE_URL, loadTokens } from "@/lib/api";
import { useUiStore } from "@/stores/useAppStore";
import type { Marble, Paginated, QueueEvent } from "@/lib/types";

const MAX_BACKOFF_MS = 30_000;

/**
 * Subscribes to the live queue WebSocket and surgically patches the TanStack
 * Query cache. The socket supplies only the "push" half of the experience —
 * pagination, retries and optimistic updates stay owned by Query.
 */
export function useQueueFeed(): void {
  const queryClient = useQueryClient();
  const setWsStatus = useUiStore((s) => s.setWsStatus);
  const enqueueMarble = useUiStore((s) => s.enqueueMarble);

  const socketRef = useRef<WebSocket | null>(null);
  const attemptRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const closedRef = useRef(false);

  useEffect(() => {
    closedRef.current = false;

    const handleEvent = (event: QueueEvent) => {
      switch (event.type) {
        case "connection.ready":
          setWsStatus("connected");
          break;

        case "marble.created": {
          const marble = event.payload as Marble;
          // Feed the physics canvas.
          enqueueMarble(marble);

          // Prepend into every cached marble list page-0 query.
          queryClient.setQueriesData<Paginated<Marble>>(
            { queryKey: ["marbles"], exact: false },
            (old) => {
              if (!old?.items) return old;
              if (old.items.some((m) => m.id === marble.id)) return old;
              return { ...old, items: [marble, ...old.items].slice(0, 200) };
            },
          );
          queryClient.invalidateQueries({ queryKey: ["jar-status"] });
          break;
        }

        case "marble.updated": {
          const marble = event.payload as Marble;
          queryClient.setQueriesData<Paginated<Marble>>(
            { queryKey: ["marbles"], exact: false },
            (old) =>
              old?.items
                ? { ...old, items: old.items.map((m) => (m.id === marble.id ? marble : m)) }
                : old,
          );
          queryClient.invalidateQueries({ queryKey: ["marble", marble.id] });
          queryClient.invalidateQueries({ queryKey: ["objectives"] });
          break;
        }

        case "marble.deleted": {
          const { id } = event.payload as { id: string };
          queryClient.setQueriesData<Paginated<Marble>>(
            { queryKey: ["marbles"], exact: false },
            (old) =>
              old?.items ? { ...old, items: old.items.filter((m) => m.id !== id) } : old,
          );
          break;
        }

        case "objective.created":
        case "objective.updated":
        case "objective.deleted":
        case "objective.shipped":
          queryClient.invalidateQueries({ queryKey: ["objectives"] });
          queryClient.invalidateQueries({ queryKey: ["objective"] });
          break;

        case "dispatch.queued":
        case "dispatch.updated":
          queryClient.invalidateQueries({ queryKey: ["dispatches"] });
          queryClient.invalidateQueries({ queryKey: ["audit"] });
          break;
      }
    };

    const connect = () => {
      if (closedRef.current) return;

      const token = loadTokens()?.access_token;
      if (!token) {
        setWsStatus("polling");
        return;
      }

      const url =
        API_BASE_URL.replace(/^http/, "ws") + `/v1/ws/queue?token=${encodeURIComponent(token)}`;

      setWsStatus(attemptRef.current === 0 ? "connecting" : "reconnecting");

      let socket: WebSocket;
      try {
        socket = new WebSocket(url);
      } catch {
        scheduleReconnect();
        return;
      }
      socketRef.current = socket;

      socket.onopen = () => {
        attemptRef.current = 0;
        setWsStatus("connected");
      };

      socket.onmessage = (ev) => {
        try {
          handleEvent(JSON.parse(ev.data) as QueueEvent);
        } catch {
          // A malformed frame must not tear down the feed.
        }
      };

      socket.onerror = () => {
        // onclose always follows; reconnection is handled there.
      };

      socket.onclose = () => {
        socketRef.current = null;
        if (closedRef.current) return;
        scheduleReconnect();
      };
    };

    const scheduleReconnect = () => {
      attemptRef.current += 1;
      // After repeated failures, tell the UI we've fallen back to polling.
      setWsStatus(attemptRef.current > 3 ? "polling" : "reconnecting");

      const base = Math.min(2 ** attemptRef.current * 500, MAX_BACKOFF_MS);
      const delay = base / 2 + Math.random() * (base / 2);
      timerRef.current = setTimeout(connect, delay);
    };

    connect();

    return () => {
      closedRef.current = true;
      if (timerRef.current) clearTimeout(timerRef.current);
      socketRef.current?.close();
      socketRef.current = null;
    };
  }, [queryClient, setWsStatus, enqueueMarble]);
}
