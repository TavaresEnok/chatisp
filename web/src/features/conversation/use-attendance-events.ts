import { useEffect, useEffectEvent, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { components } from "@/api/schema";
import { useSessionStore } from "@/stores/session-store";
import { clearBrowserStorage } from "@/lib/browser-storage";
import type { NotificationBatch } from "@/features/notifications/use-notification-events";
import { queryKeys } from "@/lib/query-keys";
import { api } from "@/api/client";

export function useAttendanceEvents(inbox: string | null, key: readonly string[], onBatch?: (batch: NotificationBatch) => void) {
  const client = useQueryClient();
  const [status, setStatus] = useState<"connected" | "disconnected" | "resync" | "degraded" | "revoked" | "checking" | "unavailable">("disconnected");
  const keyString = JSON.stringify(key);
  const deliverBatch = useEffectEvent((batch: NotificationBatch) => { onBatch?.(batch); });
  useEffect(() => {
    if (!inbox || typeof WebSocket === "undefined") return;
    let stopped = false, recovering = false, ready = false, timer: ReturnType<typeof setTimeout> | undefined;
    let authorizationRecoveryUsed = false;
    const verification = new AbortController();
    const key = JSON.parse(keyString) as string[];
    const scope = `${key.slice(1, 4).join(":")}:${inbox}`;
    let socket: WebSocket;
    const heartbeat = setInterval(() => { if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "ping" })); }, 30000);
    const seen = new Set<string>();
    const invalidate = () => { const currentKey = JSON.parse(keyString) as string[]; void client.invalidateQueries({ queryKey: currentKey }); void client.invalidateQueries({ queryKey: ["m3b-queue", ...currentKey.slice(1, 6)] }); void client.invalidateQueries({ queryKey: ["m3b-annotations", ...currentKey.slice(1, 6)] }); void client.invalidateQueries({ queryKey: queryKeys.contactHistory(currentKey.slice(1, 6)) }); };
    function connect() {
      const connection = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/panel/ws`);
      socket = connection;
      let retired = false;
      connection.onopen = () => { if (stopped || retired) return; ready = false; setStatus("connected"); const command: components["schemas"]["ClientMessage"] = { type: "subscribe", inbox_id: inbox!, with_snapshot: true }; connection.send(JSON.stringify(command)); };
      socket.onmessage = (event) => {
        if (stopped || retired) return;
        let msg: components["schemas"]["ServerMessage"];
        try { msg = JSON.parse(String(event.data)) as typeof msg; } catch { return; }
        if (msg.inbox_id && msg.inbox_id !== inbox) return;
        if (msg.type === "resync") { ready = false; seen.clear(); invalidate(); if (msg.reason === "load_limit_exceeded" || recovering) { setStatus("degraded"); return; } recovering = true; setStatus("resync"); socket.send(JSON.stringify({ type: "snapshot", inbox_id: inbox })); return; }
        if (msg.type === "snapshot") { recovering = false; ready = true; setStatus("connected"); seen.clear(); invalidate(); return; }
        const fresh = (msg.events ?? []).filter((item) => { if (seen.has(item.ID)) return false; seen.add(item.ID); return true; });
        const events = fresh.flatMap(item => item.Type === "conversation.opened" || item.Type === "conversation.message" ? [{ id: item.ID, cursor: item.Seq, kind: item.Type === "conversation.opened" ? "new_conversation" as const : "new_message" as const, live: ready && !(item.Type === "conversation.message" && typeof item.Payload === "object" && item.Payload !== null && "historical" in item.Payload && item.Payload.historical === true) }] : []).sort((a, b) => a.cursor - b.cursor);
        if (events.length) deliverBatch({ scope, events });
        if (fresh.length) invalidate();
        if (seen.size > 2000) seen.clear();
        if (msg.type === "error") invalidate();
      };
      socket.onclose = (event) => {
        if (stopped) return;
        if (retired) return;
        retired = true;
        ready = false;
        setStatus("disconnected");
        if (event.code === 1008) {
          if (timer) clearTimeout(timer);
          if (event.reason === "authorization_revoked") {
            purgeScope();
            setStatus("checking");
            void verifySession();
            return;
          }
          stopped = true;
          clearInterval(heartbeat);
          if (event.reason !== "authorization_lost") {
            // Policy closes also signal backpressure/resync. Keep HTTP actions
            // and drafts usable, without an automatic reconnect loop.
            invalidate(); setStatus("degraded"); return;
          }
          purgeScope();
          setStatus("revoked");
          return;
        }
        timer = setTimeout(connect, 1500);
      };
    }
    function purgeScope() {
      void client.cancelQueries({ queryKey: key }); client.removeQueries({ queryKey: key });
      for (const prefix of ["m3b-queue", "m3b-annotations", "m3b-candidates", "m3b-sectors", "m3b-directory"]) {
        const scoped = [prefix, ...key.slice(1, 6), inbox!];
        void client.cancelQueries({ queryKey: scoped }); client.removeQueries({ queryKey: scoped });
      }
      const contacts = queryKeys.contactHistory(key.slice(1, 6));
      void client.cancelQueries({ queryKey: contacts }); client.removeQueries({ queryKey: contacts });
    }
    async function verifySession() {
      const original = useSessionStore.getState().principal;
      try {
        // loadCurrentSession collapses 5xx into null. Inspect the real auth
        // response here: a network/server failure is not evidence of logout.
        const result = await api.GET("/api/panel/session", { signal: verification.signal });
        if (stopped || useSessionStore.getState().principal !== original) return;
        if (result.response.status === 401 || result.response.status === 403) {
          clearInterval(heartbeat);
          clearBrowserStorage(); client.clear(); useSessionStore.getState().markSessionEnded(); return;
        }
        const session = result.data;
        if (!result.response.ok || !session || !original || session.session_id !== original.sessionId || session.tenant_id !== original.tenantId || session.user_id !== original.userId || session.membership_id !== original.membershipId || session.role !== original.role) {
          clearInterval(heartbeat); setStatus("unavailable"); return;
        }
        if (authorizationRecoveryUsed) { clearInterval(heartbeat); setStatus("unavailable"); return; }
        authorizationRecoveryUsed = true;
        recovering = false; seen.clear();
        // A fresh subscription builds its snapshot from currently authorized
        // conversations. Never replay the old connection's conversation set.
        invalidate(); connect();
      } catch {
        if (!stopped) { clearInterval(heartbeat); setStatus("unavailable"); }
      }
    }
    connect();
    return () => { stopped = true; verification.abort(); clearInterval(heartbeat); if (timer) clearTimeout(timer); socket.close(); };
  }, [inbox, keyString, client]);
  return status;
}
