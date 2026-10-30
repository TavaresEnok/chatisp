import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "@/api/attendance";
import type { PanelPrincipal } from "@/stores/session-store";
import type { CloseAttendanceCommand } from "@/features/conversation/workspace-types";
import type { SendTextCommand, SendTextResult } from "@/features/conversation/types";
import { card, timeline } from "@/features/conversation/attendance-adapter";
import { useAttendanceEvents } from "@/features/conversation/use-attendance-events";

export function useAttendance(principal: PanelPrincipal) {
  const base = ["attendance", principal.tenantId, principal.userId, principal.sessionId];
  const client = useQueryClient();
  const [inbox, setInbox] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const intents = useRef(new Map<string, number>());
  const reopening = useRef(new Map<string, string>());
  const assuming = useRef(new Map<string, number>());
  const boxes = useQuery({ queryKey: [...base, "inboxes"], queryFn: api.listInboxes });
  const list = useQuery({ queryKey: [...base, inbox ?? "", "snapshot"], queryFn: () => api.snapshot(inbox!), enabled: !!inbox });
  const detail = useQuery({ queryKey: [...base, inbox ?? "", "history", selected ?? ""], queryFn: () => api.history(inbox!, selected!), enabled: !!inbox && !!selected });
  const realtime = useAttendanceEvents(inbox, [...base, inbox ?? ""]);
  useEffect(() => () => { client.removeQueries({ queryKey: base }); }, [client, principal.tenantId, principal.userId, principal.sessionId]);
  function selectInbox(id: string) { setInbox(id || null); setSelected(null); intents.current.clear(); reopening.current.clear(); assuming.current.clear(); }
  function version(id: string) { return (detail.data?.head.id === id ? detail.data.head : list.data?.conversations?.find((h) => h.id === id))?.version; }
  function intentVersion(key: string, id: string) { const old = intents.current.get(key); if (old !== undefined) return old; const current = version(id); if (current !== undefined) intents.current.set(key, current); return current; }
  const refresh = () => void client.invalidateQueries({ queryKey: [...base, inbox ?? ""] });
  function failure(error: unknown) { if (error instanceof api.AttendanceError) { if (error.status === 409) { refresh(); return { outcome: "conflict" as const, detail: "A conversa mudou. Os dados foram atualizados." }; } return { outcome: "failed" as const, detail: error.message }; } return { outcome: "failed" as const, detail: "A resposta não chegou; repita a mesma intenção." }; }
  async function assume(id: string) { const v = assuming.current.get(id) ?? version(id); if (!inbox || v === undefined) return failure(null); assuming.current.set(id, v); try { await api.assume(inbox, id, { expected_version: v }); assuming.current.delete(id); refresh(); return { outcome: "done" }; } catch (e) { if (e instanceof api.AttendanceError) assuming.current.delete(id); return failure(e); } }
  async function send(id: string, command: SendTextCommand): Promise<SendTextResult> { const v = intentVersion(command.intentKey, id); if (!inbox || v === undefined) return { outcome: "failed", detail: "Versão indisponível." }; try { const result = await api.send(inbox, id, { expected_version: v, client_request_id: command.intentKey, body: command.text }); refresh(); if (result.state === "uncertain") return { outcome: "uncertain", detail: "O resultado do envio é incerto." }; if (result.state === "failed" || result.state === "cancelled") return { outcome: "failed", detail: result.state === "failed" ? "O envio falhou." : "O envio foi cancelado." }; if (!["queued", "sending", "retrying", "accepted", "sent"].includes(result.state)) return { outcome: "uncertain", detail: "Estado do envio desconhecido." }; intents.current.delete(command.intentKey); return { outcome: "accepted" }; } catch (e) { if (!(e instanceof api.AttendanceError)) return { outcome: "lost" }; intents.current.delete(command.intentKey); const result = failure(e); return { outcome: "failed", detail: result.detail }; } }
  async function close(command: CloseAttendanceCommand) { const v = intentVersion(command.clientRequestId, command.conversationId); if (!inbox || v === undefined) return failure(null); try { const result = await api.close(inbox, command.conversationId, { expected_version: v, client_request_id: command.clientRequestId, reason: command.reason, result: command.result, summary: command.synthesis, ...(command.farewellBody ? { farewell_body: command.farewellBody, farewell_request_id: command.farewellRequestId } : {}) }); intents.current.delete(command.clientRequestId); refresh(); return { outcome: "done", farewellFailed: result.farewell_failed || (!!command.farewellBody && !result.farewell) }; } catch (e) { if (e instanceof api.AttendanceError) { intents.current.delete(command.clientRequestId); return failure(e); } return { outcome: "lost", detail: "A resposta não chegou; repita a mesma intenção." }; } }
  async function reopen(id: string) { const key = reopening.current.get(id) ?? crypto.randomUUID(); reopening.current.set(id, key); const v = intentVersion(key, id); if (!inbox || v === undefined) return failure(null); try { await api.reopen(inbox, id, { expected_version: v, client_request_id: key }); reopening.current.delete(id); refresh(); return { outcome: "done" }; } catch (e) { if (e instanceof api.AttendanceError) reopening.current.delete(id); return failure(e); } }
  return { boxes, inbox, selectInbox, selected, setSelected, list, detail, realtime, cards: (list.data?.conversations ?? []).map(card), entries: detail.data ? timeline(detail.data) : [], assume, send, close, reopen };
}
