import { useEffect, useRef, useState } from "react";
import { MessageComposer } from "@/features/conversation/message-composer";
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
  createIntentKey?: () => string;
  onSelect: (conversationId: string) => void;
  onAssume: (conversationId: string) => Promise<unknown> | unknown;
  onSend: (
    conversationId: string,
    command: SendTextCommand,
  ) => Promise<SendTextResult> | SendTextResult;
  onClose: (command: CloseAttendanceCommand) => Promise<unknown> | unknown;
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
  createIntentKey,
  onSelect,
  onAssume,
  onSend,
  onClose,
  onFarewell,
}: ConversationWorkspaceProps) {
  const [session, setSession] = useState<Session>({
    selectedId: null,
    opened: [],
    card: null,
  });
  const [assumeState, setAssumeState] = useState<AssumeState | null>(null);
  const [closeOpen, setCloseOpen] = useState(false);
  const alive = useRef(true);
  const assumeRef = useRef<AssumeState | null>(null);
  assumeRef.current = assumeState;
  const synced = syncSession(session, listStatus, conversations);
  if (synced !== session) setSession(synced);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const card = synced.card;
  const assumeForCard =
    card !== null && assumeState !== null && assumeState.conversationId === card.id
      ? assumeState
      : null;
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

  async function assume() {
    if (!card || card.phase === "closed") return;
    const conversationId = card.id;
    const current = assumeRef.current;
    if (current?.conversationId === conversationId && current.pending) return;
    const pending = { conversationId, pending: true, notice: null };
    assumeRef.current = pending;
    setAssumeState(pending);
    let response: unknown;
    try {
      response = await Promise.resolve(onAssume(conversationId));
    } catch {
      response = null;
    }
    if (!alive.current) return;
    const action = readAttendanceResult(response);
    const resolved = {
      conversationId,
      pending: false,
      notice: action.outcome === "done" ? null : action.detail,
    };
    setAssumeState((state) => {
      if (!state || state.conversationId !== conversationId) return state;
      return resolved;
    });
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(16rem,22rem)_minmax(0,1fr)]">
      <ConversationList
        status={listStatus}
        conversations={conversations}
        selectedId={card?.id ?? null}
        onSelect={selectConversation}
      />
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
            <MessageTimeline
              scope={{ ...scope, conversationId: card.id }}
              protocol={card.protocol}
              status={timelineStatus}
              entries={timelineEntries}
            />
            {card.phase === "closed" ? (
              <p className="text-sm text-muted-foreground">
                O compositor fica indisponível após o encerramento.
              </p>
            ) : null}
            {synced.opened.map((item) => (
              <div key={item.id} hidden={item.id !== card.id || card.phase === "closed"}>
                <MessageComposer
                  scope={{ ...scope, conversationId: item.id }}
                  offline={offline}
                  createIntentKey={createIntentKey}
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
