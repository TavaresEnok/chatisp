import { ContactDocument } from "@/features/contactdocs/contact-document";
import { useCallback, useContext, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { QueryClientContext, type QueryClient } from "@tanstack/react-query";
import { downloadAttachment } from "@/api/attachments";
import { AttachmentDesk } from "@/features/attachments/attachment-desk";
import { ConsentForm, TemplateCatalog, WindowIndicator } from "@/features/channelpolicy/channel-panels";
import { MessageComposer } from "@/features/conversation/message-composer";
import type { PanelPrincipal } from "@/stores/session-store";
import { MessageTimeline } from "@/features/conversation/message-timeline";
import { CloseConversationDialog } from "@/features/conversation/close-conversation-dialog";
import { ConversationHeader } from "@/features/conversation/conversation-header";
import { ConversationList } from "@/features/conversation/conversation-list";
import type { ReplyTarget, SendAudioCommand, SendMediaCommand, SendTextCommand, SendTextResult, TimelineEntry, TimelineViewStatus } from "@/features/conversation/types";
import { audioFileName } from "@/features/media/model";
import { deliverOutboundMedia, UnconfirmedMediaSend } from "@/features/media/send-media";
import { attendanceQueryBase, invalidateAttendanceQueries } from "@/features/conversation/use-attendance";
import { readAttendanceResult } from "@/features/conversation/workspace-types";
import type {
  AttendanceScope,
  CloseAttendanceCommand,
  ConversationCard,
  ConversationListStatus,
} from "@/features/conversation/workspace-types";

export type ConversationWorkspaceProps = {
  scope: AttendanceScope;
  listStatus: ConversationListStatus;
  conversations: readonly ConversationCard[];
  detailConversationId: string | null;
  detailStatus: TimelineViewStatus;
  entries: readonly TimelineEntry[];
  offline?: boolean;
  localQueue?: boolean;
  createIntentKey?: () => string;
  onSelect: (conversationId: string) => void;
  onAssume: (conversationId: string) => Promise<unknown> | unknown;
  onSend: (
    conversationId: string,
    command: SendTextCommand,
  ) => Promise<SendTextResult> | SendTextResult;
  onClose: (command: CloseAttendanceCommand) => Promise<unknown> | unknown;
  onReopen?: (conversationId: string) => Promise<unknown> | unknown;
  onSearch?: () => void;
  onNote?: (conversationId: string) => void;
  renderQueue?: (selectedId: string | null, select: (id: string) => void) => ReactNode;
  renderActions?: (conversationId: string) => ReactNode;
  renderSide?: (conversationId: string) => ReactNode;
  onTyping?: (conversationId: string) => void;
  inboxId?: string;
  connectorType?: string;
  contactId?: string | null;
  conversationVersion?: number | null;
  principal?: PanelPrincipal | null;
  onFarewell: (
    conversationId: string,
    command: SendTextCommand,
  ) => Promise<SendTextResult> | SendTextResult;
};

type Session = {
  selectedId: string | null;
  opened: readonly ConversationCard[];
  card: ConversationCard | null;
};

type AssumeState = {
  conversationId: string;
  pending: boolean;
  notice: string | null;
};

function cachedConversationVersion(client: QueryClient, principal: PanelPrincipal, inboxId: string, conversationId: string): number | null {
  const base = attendanceQueryBase(principal);
  const histories = client.getQueriesData<{ head?: { id?: string; version?: number } }>({ queryKey: [...base, inboxId, "history", conversationId] });
  for (const [, data] of histories) {
    if (data?.head?.id === conversationId && typeof data.head.version === "number") return data.head.version;
  }
  const snapshots = client.getQueriesData<{ conversations?: { id?: string; version?: number }[] }>({ queryKey: [...base, inboxId, "snapshot"] });
  for (const [, data] of snapshots) {
    const match = data?.conversations?.find((item) => item.id === conversationId);
    if (match && typeof match.version === "number") return match.version;
  }
  return null;
}

function attendanceScopeKey(scope: AttendanceScope): string {
  return `${scope.tenantId}\u001f${scope.userId}`;
}

function sameCard(left: ConversationCard, right: ConversationCard): boolean {
  return (
    left.id === right.id &&
    left.protocol === right.protocol &&
    left.contactLabel === right.contactLabel &&
    left.phase === right.phase &&
    left.assigneeLabel === right.assigneeLabel
  );
}

function syncSession(
  state: Session,
  listStatus: ConversationListStatus,
  conversations: readonly ConversationCard[],
): Session {
  if (listStatus !== "ready") return state;
  const byId = new Map(conversations.map((item) => [item.id, item]));
  let changed = false;
  const opened: ConversationCard[] = [];
  for (const item of state.opened) {
    const live = byId.get(item.id);
    if (!live) {
      changed = true;
      continue;
    }
    if (!sameCard(item, live)) changed = true;
    opened.push(live);
  }
  const liveSelected = state.selectedId ? (byId.get(state.selectedId) ?? null) : null;
  if (!liveSelected) {
    if (state.selectedId === null && state.card === null && !changed) return state;
    return { selectedId: null, opened, card: null };
  }
  if (!changed && state.card && sameCard(state.card, liveSelected)) return state;
  return { selectedId: liveSelected.id, opened, card: liveSelected };
}

export function ConversationWorkspace(props: ConversationWorkspaceProps) {
  return <WorkspaceSession key={attendanceScopeKey(props.scope)} {...props} />;
}

function WorkspaceSession({
  scope,
  listStatus,
  conversations,
  detailConversationId,
  detailStatus,
  entries,
  offline = false,
  localQueue = false,
  createIntentKey,
  onSelect,
  onAssume,
  onSend,
  onClose,
  onReopen,
  onFarewell,
  onSearch,
  onNote,
  renderQueue,
  renderActions,
  renderSide,
  onTyping,
  inboxId,
  connectorType = "",
  contactId = null,
  conversationVersion = null,
  principal = null,
}: ConversationWorkspaceProps) {
  const [session, setSession] = useState<Session>({
    selectedId: null,
    opened: [],
    card: null,
  });
  const [assumeStates, setAssumeStates] = useState<Record<string, AssumeState>>({});
  const [closeOpen, setCloseOpen] = useState(false);
  const alive = useRef(true);
  const workspace = useRef<HTMLDivElement>(null);
  const assumingIds = useRef(new Set<string>());
  const mediaVersions = useRef(new Map<string, number>());
  const queryClient = useContext(QueryClientContext);
  const [replyTo, setReplyTo] = useState<ReplyTarget | null>(null);
  const replyConversation = useRef<string | null>(null);
  const synced = syncSession(session, listStatus, conversations);
  if (synced !== session) setSession(synced);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const card = synced.card;
  if ((card?.id ?? null) !== replyConversation.current) {
    replyConversation.current = card?.id ?? null;
    if (replyTo) setReplyTo(null);
  }
  const assumeForCard = card ? assumeStates[card.id] : null;
  const assuming = assumeForCard?.pending ?? false;
  const assumeNotice = assumeForCard && !assumeForCard.pending ? assumeForCard.notice : null;
  const selectedId = card?.id ?? "";
  const loadDownload = useCallback(
    async (attachmentId: string) => {
      if (!inboxId || !selectedId) throw new Error("A caixa desta conversa não está disponível.");
      const ticket = await downloadAttachment(inboxId, selectedId, attachmentId);
      return { url: ticket.url, expiresIn: ticket.expires_in };
    },
    [inboxId, selectedId],
  );
  const detailMatches = card !== null && detailConversationId === card.id;
  const timelineStatus = detailMatches ? detailStatus : "loading";
  const timelineEntries = detailMatches && detailStatus === "ready" ? entries : [];

  function selectConversation(conversationId: string) {
    if (listStatus !== "ready") return;
    const next = conversations.find((item) => item.id === conversationId);
    if (!next) return;
    setCloseOpen(false);
    setSession((current) => {
      const opened = current.opened.some((item) => item.id === next.id)
        ? current.opened.map((item) => (item.id === next.id ? next : item))
        : [...current.opened, next];
      return { selectedId: next.id, opened, card: next };
    });
    onSelect(next.id);
  }

  function handleShortcut(event: React.KeyboardEvent<HTMLDivElement>) {
    if (closeOpen || event.defaultPrevented || event.nativeEvent.isComposing || !(event.ctrlKey || event.metaKey) || !event.altKey) return;
    const target = event.target as HTMLElement;
    if (target.closest("input, textarea, select, [contenteditable=true], [role=dialog]")) return;
    if (event.key.toLowerCase() === "j" && conversations.length && listStatus === "ready") {
      event.preventDefault();
      const index = conversations.findIndex((item) => item.id === card?.id);
      const next = conversations[(index + 1) % conversations.length];
      if (next) {
        selectConversation(next.id);
        workspace.current?.querySelectorAll<HTMLElement>('[role="option"]')[(index + 1) % conversations.length]?.focus();
      }
    } else if (event.key.toLowerCase() === "f") {
      const field = workspace.current?.querySelector<HTMLInputElement>('input[type="search"]');
      if (onSearch || field) { event.preventDefault(); if (onSearch) onSearch(); else field?.focus(); }
    } else if (event.key.toLowerCase() === "n" && card) {
      const field = workspace.current?.querySelector<HTMLTextAreaElement>('[aria-label="Anotações privadas"] textarea');
      if (onNote || field) { event.preventDefault(); if (onNote) onNote(card.id); else field?.focus(); }
    }
  }

  function versionFor(conversationId: string, intentKey: string): number | null {
    const pinned = mediaVersions.current.get(intentKey);
    if (pinned !== undefined) return pinned;
    const cached = queryClient && principal && inboxId ? cachedConversationVersion(queryClient, principal, inboxId, conversationId) : null;
    const fromProp = card && conversationId === card.id && detailConversationId === card.id && typeof conversationVersion === "number" ? conversationVersion : null;
    const current = cached ?? fromProp;
    if (current === null) return null;
    mediaVersions.current.set(intentKey, current);
    return current;
  }

  async function sendMedia(conversationId: string, command: SendMediaCommand): Promise<SendTextResult> {
    if (!inboxId) return { outcome: "failed", detail: "A caixa desta conversa não está disponível para enviar mídia." };
    const version = versionFor(conversationId, command.intentKey);
    if (version === null) return { outcome: "failed", detail: "Versão indisponível." };
    const result = await deliverOutboundMedia({ inboxId, conversationId, version, command });
    const replayable = result.outcome === "lost" || result.outcome === "uncertain";
    if (!replayable) mediaVersions.current.delete(command.intentKey);
    if (result.outcome !== "lost" && queryClient && principal) {
      try {
        await invalidateAttendanceQueries(queryClient, principal, inboxId);
      } catch {
        // A operação já terminou. Falha ao atualizar a tela não altera a chave nem o resultado.
      }
    }
    return result;
  }

  async function sendAudio(conversationId: string, command: SendAudioCommand): Promise<void> {
    const file = new File([command.blob], audioFileName(command.mimeType), { type: command.mimeType || "application/octet-stream" });
    const result = await sendMedia(conversationId, {
      intentKey: command.intentKey,
      caption: "",
      file,
      ...(command.replyToMessageId ? { replyToMessageId: command.replyToMessageId } : {}),
    });
    if (result.outcome === "accepted") return;
    if (result.outcome === "lost" || result.outcome === "uncertain") throw new UnconfirmedMediaSend(result);
    throw new Error(result.detail);
  }

  async function assume() {
    if (!card || card.phase === "closed") return;
    const conversationId = card.id;
    if (assumingIds.current.has(conversationId)) return;
    const pending = { conversationId, pending: true, notice: null };
    assumingIds.current.add(conversationId);
    setAssumeStates((states) => ({ ...states, [conversationId]: pending }));
    let response: unknown;
    try {
      response = await Promise.resolve(onAssume(conversationId));
    } catch {
      response = null;
    }
    assumingIds.current.delete(conversationId);
    if (!alive.current) return;
    const action = readAttendanceResult(response);
    const resolved = {
      conversationId,
      pending: false,
      notice: action.outcome === "done" ? null : action.detail,
    };
    setAssumeStates((states) => ({ ...states, [conversationId]: resolved }));
  }

  return (
    <div ref={workspace} className="grid gap-4 lg:grid-cols-[minmax(16rem,22rem)_minmax(0,1fr)] lg:has-[.painel-externo]:grid-cols-[minmax(16rem,20rem)_minmax(0,1fr)_minmax(18rem,24rem)]" onKeyDown={handleShortcut}>
      {renderQueue ? renderQueue(card?.id ?? null, selectConversation) : <ConversationList
        status={listStatus}
        conversations={conversations}
        selectedId={card?.id ?? null}
        onSelect={selectConversation}
      />}
      <div className="flex min-w-0 flex-col gap-4">
        {card ? (
          <>
            <ConversationHeader
              contactLabel={card.contactLabel}
              protocol={card.protocol}
              phase={card.phase}
              assigneeLabel={card.assigneeLabel}
              assuming={assuming}
              notice={assumeNotice}
              onAssume={() => void assume()}
              onClose={() => setCloseOpen(true)}
            />
            {renderActions?.(card.id)}
            <MessageTimeline
              scope={{ ...scope, conversationId: card.id }}
              protocol={card.protocol}
              status={timelineStatus}
              entries={timelineEntries}
              onReply={setReplyTo}
              loadDownload={inboxId ? loadDownload : undefined}
            />
            {principal && inboxId ? (
              <>
                {contactId && detailMatches && <ContactDocument key={`document:${card.id}:${contactId}`} principal={principal} contactId={contactId} />}
 <WindowIndicator connectorType={connectorType} />
                <TemplateCatalog key={`templates:${card.id}`} principal={principal} inboxId={inboxId} conversationId={card.id} connectorType={connectorType} version={detailMatches ? conversationVersion : null} />
                {contactId && detailMatches ? <ConsentForm key={`consent:${card.id}:${contactId}`} principal={principal} inboxId={inboxId} contactId={contactId} /> : null}
                <AttachmentDesk key={`attachments:${card.id}`} principal={principal} inboxId={inboxId} conversationId={card.id} closed={card.phase === "closed"} />
              </>
            ) : null}
            {card.phase === "closed" ? (
              <div><p className="text-sm text-muted-foreground">
                O compositor fica indisponível após o encerramento.
              </p>{onReopen ? <button type="button" onClick={async () => { const result = readAttendanceResult(await onReopen(card.id)); if (result.outcome !== "done") setAssumeStates((states) => ({ ...states, [card.id]: { conversationId: card.id, pending: false, notice: result.detail } })); }}>Reabrir</button> : null}</div>
            ) : null}
            {synced.opened.map((item) => (
              <div key={item.id} hidden={item.id !== card.id || card.phase === "closed"}>
                <MessageComposer
                  scope={{ ...scope, conversationId: item.id }}
                  offline={offline}
                  localQueue={localQueue}
                  createIntentKey={createIntentKey}
                  replyInboxId={inboxId}
                  replyTo={item.id === card.id ? replyTo : null}
                  onClearReply={() => setReplyTo(null)}
                  onTyping={() => onTyping?.(item.id)}
                  onSend={(command) => onSend(item.id, command)}
                  onSendMedia={(command) => sendMedia(item.id, command)}
                  onSendAudio={(command) => sendAudio(item.id, command)}
                />
              </div>
            ))}
          </>
        ) : (
          <p className="text-sm text-muted-foreground">
            Selecione uma conversa para ver o protocolo e o histórico.
          </p>
        )}
      </div>
      {renderSide && card ? <div className="contents">{renderSide(card.id)}</div> : null}
      {closeOpen && card ? (
        <CloseConversationDialog
          protocol={card.protocol}
          createIntentKey={createIntentKey}
          onClose={(fields) => onClose({ ...fields, conversationId: card.id })}
          onFarewell={(command) => onFarewell(card.id, command)}
          onDismiss={() => setCloseOpen(false)}
        />
      ) : null}
    </div>
  );
}
