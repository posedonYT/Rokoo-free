"use client";

import { wsBase } from "@/shared/api/transport";

export type RealtimeEvent = {
  type: string;
  event_type?: string;
  payload?: {
    type?: string;
    guild_id?: string;
    channel_id?: string;
    payload?: Record<string, string>;
  };
  channel_id?: string;
  user_id?: string;
  status?: string;
  online?: Record<string, boolean>;
};

export function connectRealtime(token: string, onEvent: (event: RealtimeEvent) => void) {
  const socket = new WebSocket(`${wsBase}?token=${encodeURIComponent(token)}`);
  const send = (body: unknown) => {
    if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(body));
  };
  socket.onmessage = (ev) => {
    try {
      onEvent(JSON.parse(String(ev.data)) as RealtimeEvent);
    } catch {}
  };
  socket.onopen = () => send({ type: "heartbeat" });
  const beat = window.setInterval(() => send({ type: "heartbeat" }), 20000);
  return {
    send,
    close() {
      window.clearInterval(beat);
      socket.close();
    },
  };
}
