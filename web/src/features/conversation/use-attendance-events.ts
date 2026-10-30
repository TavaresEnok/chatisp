import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { components } from "@/api/schema";
import { useSessionStore } from "@/stores/session-store";

export function useAttendanceEvents(inbox: string | null, key: readonly string[]) {
  const client = useQueryClient();
  const [status, setStatus] = useState<"connected" | "disconnected" | "resync" | "degraded">("disconnected");
  const keyString = JSON.stringify(key);
  useEffect(() => {
    if (!inbox || typeof WebSocket === "undefined") return;
    let stopped = false, cursor = 0, recovering = false, timer: ReturnType<typeof setTimeout> | undefined;
    let socket: WebSocket;
    const heartbeat = setInterval(() => { if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "ping" })); }, 30000);
    const seen = new Set<string>();
    const invalidate = () => void client.invalidateQueries({ queryKey: JSON.parse(keyString) as string[] });
    function connect() {
      socket = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/panel/ws`);
      socket.onopen = () => { setStatus("connected"); const command: components["schemas"]["ClientMessage"] = { type: "subscribe", inbox_id: inbox!, ...(cursor ? { after: cursor } : { with_snapshot: true }) }; socket.send(JSON.stringify(command)); };
      socket.onmessage = (event) => {
        let msg: components["schemas"]["ServerMessage"];
        try { msg = JSON.parse(String(event.data)) as typeof msg; } catch { return; }
        if (msg.inbox_id && msg.inbox_id !== inbox) return;
        if (msg.type === "resync") { cursor = 0; seen.clear(); invalidate(); if (msg.reason === "load_limit_exceeded" || recovering) { setStatus("degraded"); return; } recovering = true; setStatus("resync"); socket.send(JSON.stringify({ type: "snapshot", inbox_id: inbox })); return; }
        if (msg.type === "snapshot") { recovering = false; setStatus("connected"); cursor = msg.next_cursor ?? 0; seen.clear(); invalidate(); return; }
        if (typeof msg.next_cursor === "number") cursor = Math.max(cursor, msg.next_cursor);
        const fresh = (msg.events ?? []).filter((item) => { if (seen.has(item.ID)) return false; seen.add(item.ID); return true; });
        if (fresh.length) invalidate();
        if (seen.size > 2000) seen.clear();
        if (msg.type === "error") invalidate();
      };
      socket.onclose = (event) => { if (stopped) return; setStatus("disconnected"); if (event.code === 1008 && (event.reason === "authorization_revoked" || event.reason === "authorization_lost")) { client.removeQueries({ queryKey: (JSON.parse(keyString) as string[]).slice(0, 4) }); useSessionStore.getState().markSessionEnded(); return; } timer = setTimeout(connect, 1500); };
    }
    connect();
    return () => { stopped = true; clearInterval(heartbeat); if (timer) clearTimeout(timer); socket.close(); };
  }, [inbox, keyString, client]);
  return status;
}
