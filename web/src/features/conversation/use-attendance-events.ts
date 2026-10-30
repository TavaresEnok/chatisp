import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { components } from "@/api/schema";
import { useSessionStore } from "@/stores/session-store";
import { clearBrowserStorage } from "@/lib/browser-storage";
import type { NotificationBatch } from "@/features/notifications/use-notification-events";
import { queryKeys } from "@/lib/query-keys";

export function useAttendanceEvents(inbox: string | null, key: readonly string[], onBatch?: (batch: NotificationBatch) => void) {
  const client = useQueryClient();
  const [status, setStatus] = useState<"connected" | "disconnected" | "resync" | "degraded">("disconnected");
  const keyString = JSON.stringify(key);
  useEffect(() => {
    if (!inbox || typeof WebSocket === "undefined") return;
    let stopped = false, recovering = false, ready = false, timer: ReturnType<typeof setTimeout> | undefined;
    const scope = `${key.slice(1, 4).join(":")}:${inbox}`;
    let socket: WebSocket;
    const heartbeat = setInterval(() => { if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "ping" })); }, 30000);
    const seen = new Set<string>();
    const invalidate = () => { const currentKey = JSON.parse(keyString) as string[]; void client.invalidateQueries({ queryKey: currentKey }); void client.invalidateQueries({ queryKey: queryKeys.contactHistory(currentKey.slice(1, 6)) }); };
    function connect() {
      socket = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/panel/ws`);
      socket.onopen = () => { ready = false; setStatus("connected"); const command: components["schemas"]["ClientMessage"] = { type: "subscribe", inbox_id: inbox!, with_snapshot: true }; socket.send(JSON.stringify(command)); };
      socket.onmessage = (event) => {
        if (stopped) return;
        let msg: components["schemas"]["ServerMessage"];
        try { msg = JSON.parse(String(event.data)) as typeof msg; } catch { return; }
        if (msg.inbox_id && msg.inbox_id !== inbox) return;
        if (msg.type === "resync") { ready = false; seen.clear(); invalidate(); if (msg.reason === "load_limit_exceeded" || recovering) { setStatus("degraded"); return; } recovering = true; setStatus("resync"); socket.send(JSON.stringify({ type: "snapshot", inbox_id: inbox })); return; }
        if (msg.type === "snapshot") { recovering = false; ready = true; setStatus("connected"); seen.clear(); invalidate(); return; }
        const fresh = (msg.events ?? []).filter((item) => { if (seen.has(item.ID)) return false; seen.add(item.ID); return true; });
        const events = fresh.flatMap(item => item.Type === "conversation.opened" || item.Type === "conversation.message" ? [{ id: item.ID, cursor: item.Seq, kind: item.Type === "conversation.opened" ? "new_conversation" as const : "new_message" as const, live: ready && !(item.Type === "conversation.message" && typeof item.Payload === "object" && item.Payload !== null && "historical" in item.Payload && item.Payload.historical === true) }] : []).sort((a, b) => a.cursor - b.cursor);
        if (events.length) onBatch?.({ scope, events });
        if (fresh.length) invalidate();
        if (seen.size > 2000) seen.clear();
        if (msg.type === "error") invalidate();
      };
      socket.onclose = (event) => { if (stopped) return; ready = false; setStatus("disconnected"); if (event.code === 1008 && (event.reason === "authorization_revoked" || event.reason === "authorization_lost")) { clearBrowserStorage(); client.clear(); useSessionStore.getState().markSessionEnded(); return; } timer = setTimeout(connect, 1500); };
    }
    connect();
    return () => { stopped = true; clearInterval(heartbeat); if (timer) clearTimeout(timer); socket.close(); };
  }, [inbox, keyString, client]);
  return status;
}
