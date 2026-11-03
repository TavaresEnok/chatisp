import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { AttachmentDesk } from "@/features/attachments/attachment-desk";
import { ConsentForm, TemplateCatalog, WindowIndicator } from "@/features/channelpolicy/channel-panels";
import { MessageComposer } from "@/features/conversation/message-composer";
import type { PanelPrincipal } from "@/stores/session-store";
import { MessageTimeline } from "@/features/conversation/message-timeline";
import { CloseConversationDialog } from "@/features/conversation/close-conversation-dialog";
import { ConversationHeader } from "@/features/conversation/conversation-header";
import { ConversationList } from "@/features/conversation/conversation-list";
import type { SendTextCommand, SendTextResult, TimelineEntry, TimelineViewStatus } from "@/features/conversation/types";
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
  const synced = syncSession(session, listStatus, conversations);
  if (synced !== session) setSession(synced);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const card = synced.card;
  const assumeForCard = card ? assumeStates[card.id] : null;
  const assuming = assumeForCard?.pending ?? false;
  const assumeNotice = assumeForCard && !assumeForCard.pending ? assumeForCard.notice : null;
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
    <div ref={workspace} className="grid gap-4 lg:grid-cols-[minmax(16rem,22rem)_minmax(0,1fr)]" onKeyDown={handleShortcut}>
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
            />
            {principal && inboxId ? (
              <>
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
                  onTyping={() => onTyping?.(item.id)}
                  onSend={(command) => onSend(item.id, command)}
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
