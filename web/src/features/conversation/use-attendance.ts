import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import * as api from "@/api/attendance";
import type { PanelPrincipal } from "@/stores/session-store";
import type { CloseAttendanceCommand } from "@/features/conversation/workspace-types";
import type { SendTextCommand, SendTextResult } from "@/features/conversation/types";
import { card, timeline } from "@/features/conversation/attendance-adapter";
import { textSendBody, outboundResult } from "@/features/media/send-media";
import { useAttendanceEvents } from "@/features/conversation/use-attendance-events";
import type { NotificationBatch } from "@/features/notifications/use-notification-events";
import { queryKeys } from "@/lib/query-keys";
import { clearDrafts } from "@/features/conversation/draft-store";
import { useSessionStore } from "@/stores/session-store";

// Persiste mesmo fora da página: a transição ocorre antes de outro perfil ler rascunhos.
useSessionStore.subscribe((next, previous) => {
  const before = previous.principal;
  const after = next.principal;
  if (before && after && before.tenantId === after.tenantId && before.userId === after.userId && before.sessionId === after.sessionId && (before.role !== after.role || before.membershipId !== after.membershipId)) clearDrafts();
});

export function attendanceQueryBase(principal: Pick<PanelPrincipal, "tenantId" | "userId" | "sessionId" | "role" | "membershipId">) {
  return ["attendance", principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId] as const;
}

export function invalidateAttendanceQueries(client: QueryClient, principal: Pick<PanelPrincipal, "tenantId" | "userId" | "sessionId" | "role" | "membershipId">, inboxId: string) {
  const base = attendanceQueryBase(principal);
  const scope = base.slice(1);
  return Promise.all([
    client.invalidateQueries({ queryKey: [...base, inboxId] }),
    client.invalidateQueries({ queryKey: ["m3b-queue", ...scope] }),
    client.invalidateQueries({ queryKey: queryKeys.contactHistory(scope) }),
  ]);
}

export function useAttendance(principal: PanelPrincipal) {
  const base = useMemo(() => attendanceQueryBase({
    tenantId: principal.tenantId,
    userId: principal.userId,
    sessionId: principal.sessionId,
    role: principal.role,
    membershipId: principal.membershipId,
  }), [principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId]);
  const client = useQueryClient();
  const [chosenInbox, setInbox] = useState<string | null | undefined>(undefined);
  const [selected, setSelected] = useState<string | null>(null);
  const intents = useRef(new Map<string, number>());
  const reopening = useRef(new Map<string, string>());
  const assuming = useRef(new Map<string, number>());
  const boxes = useQuery({ queryKey: [...base, "inboxes"], queryFn: api.listInboxes });
  const inbox = chosenInbox === undefined && boxes.data?.inboxes?.length === 1 ? boxes.data.inboxes[0]?.id ?? null : chosenInbox ?? null;
  const list = useQuery({ queryKey: [...base, inbox ?? "", "snapshot"], queryFn: () => api.snapshot(inbox!), enabled: !!inbox });
  const detail = useQuery({ queryKey: [...base, inbox ?? "", "history", selected ?? ""], queryFn: () => api.history(inbox!, selected!), enabled: !!inbox && !!selected });
  const [notificationBatch, setNotificationBatch] = useState<NotificationBatch | null>(null);
  const subscription = useRef(0);
  const generation = subscription.current;
  const realtime = useAttendanceEvents(inbox, [...base, inbox ?? ""], batch => { if (generation !== subscription.current) return; setNotificationBatch(previous => ({ scope: batch.scope, events: [...(previous?.scope === batch.scope ? previous.events : []), ...batch.events].slice(-2000) })); });
  useEffect(() => () => { const historyKey = queryKeys.contactHistory(base.slice(1)); void client.cancelQueries({ queryKey: historyKey }); client.removeQueries({ queryKey: historyKey }); client.removeQueries({ queryKey: base }); }, [client, base]);
  function selectInbox(id: string) { if ((id || null) !== inbox) { subscription.current++; setNotificationBatch(null); } setInbox(id || null); setSelected(null); intents.current.clear(); reopening.current.clear(); assuming.current.clear(); }
  function version(id: string) { return (detail.data?.head.id === id ? detail.data.head : list.data?.conversations?.find((h) => h.id === id))?.version; }
  function intentVersion(key: string, id: string) { const old = intents.current.get(key); if (old !== undefined) return old; const current = version(id); if (current !== undefined) intents.current.set(key, current); return current; }
  const refresh = () => { void invalidateAttendanceQueries(client, principal, inbox ?? ""); };
  function failure(error: unknown) { if (error instanceof api.AttendanceError) { if (error.status === 409) { refresh(); return { outcome: "conflict" as const, detail: "A conversa mudou. Os dados foram atualizados." }; } return { outcome: "failed" as const, detail: error.message }; } return { outcome: "failed" as const, detail: "A resposta não chegou; repita a mesma intenção." }; }
  async function assume(id: string) { const v = assuming.current.get(id) ?? version(id); if (!inbox || v === undefined) return failure(null); assuming.current.set(id, v); try { await api.assume(inbox, id, { expected_version: v }); assuming.current.delete(id); refresh(); return { outcome: "done" }; } catch (e) { if (e instanceof api.AttendanceError) assuming.current.delete(id); return failure(e); } }
  async function send(id: string, command: SendTextCommand): Promise<SendTextResult> { const v = intentVersion(command.intentKey, id); if (!inbox || v === undefined) return { outcome: "failed", detail: "Versão indisponível." }; try { const result = await api.send(inbox, id, textSendBody(v, command)); refresh(); const settled = outboundResult(result.state); if (settled.outcome === "accepted") intents.current.delete(command.intentKey); return settled; } catch (e) { if (!(e instanceof api.AttendanceError)) return { outcome: "lost" }; intents.current.delete(command.intentKey); const result = failure(e); return { outcome: "failed", detail: result.detail }; } }
  async function close(command: CloseAttendanceCommand) { const v = intentVersion(command.clientRequestId, command.conversationId); if (!inbox || v === undefined) return failure(null); try { const result = await api.close(inbox, command.conversationId, { expected_version: v, client_request_id: command.clientRequestId, reason: command.reason, result: command.result, summary: command.synthesis, ...(command.farewellBody ? { farewell_body: command.farewellBody, farewell_request_id: command.farewellRequestId } : {}) }); intents.current.delete(command.clientRequestId); refresh(); return { outcome: "done", farewellFailed: result.farewell_failed || (!!command.farewellBody && !result.farewell) }; } catch (e) { if (e instanceof api.AttendanceError) { intents.current.delete(command.clientRequestId); return failure(e); } return { outcome: "lost", detail: "A resposta não chegou; repita a mesma intenção." }; } }
  async function reopen(id: string) { const key = reopening.current.get(id) ?? crypto.randomUUID(); reopening.current.set(id, key); const v = intentVersion(key, id); if (!inbox || v === undefined) return failure(null); try { await api.reopen(inbox, id, { expected_version: v, client_request_id: key }); reopening.current.delete(id); refresh(); return { outcome: "done" }; } catch (e) { if (e instanceof api.AttendanceError) reopening.current.delete(id); return failure(e); } }
  return { boxes, inbox, selectInbox, selected, setSelected, list, detail, realtime, notificationBatch, cards: (list.data?.conversations ?? []).map(card), entries: detail.data ? timeline(detail.data) : [], assume, send, close, reopen, refresh };
}
