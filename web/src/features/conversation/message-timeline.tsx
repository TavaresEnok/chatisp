import { defaultRangeExtractor, useVirtualizer } from "@tanstack/react-virtual";
import { useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { Button } from "@/components/ui/button";
import { conversationScopeKey } from "@/features/conversation/send-intent";
import type {
  ConversationScope,
  DeliveryReceipt,
  LocalSendState,
  TimelineEntry,
  TimelineViewStatus,
} from "@/features/conversation/types";
import { formatWhen } from "@/lib/format";

const TAIL_THRESHOLD_PX = 48;
const ESTIMATE_ROW_PX = 96;

const localStateLabel: Record<LocalSendState, string> = {
  queued: "Na fila local",
  sending: "Envio em andamento",
  accepted: "Aceita pelo conector",
  failed: "Falha no envio",
  retrying: "Retentativa segura",
  uncertain: "Envio não confirmado",
  cancelled: "Cancelada antes do envio",
};

const receiptLabel: Record<DeliveryReceipt, string> = {
  delivered: "Entregue ao destinatário",
  read: "Lida pelo destinatário",
};

const localStateClass: Record<LocalSendState, string> = {
  queued: "text-muted-foreground",
  sending: "text-foreground",
  accepted: "text-primary",
  failed: "text-destructive",
  retrying: "text-foreground",
  uncertain: "text-destructive",
  cancelled: "text-muted-foreground",
};

export type MessageTimelineProps = {
  scope: ConversationScope;
  protocol: string;
  status: TimelineViewStatus;
  entries: readonly TimelineEntry[];
};

function isAwayFromTail(element: HTMLElement): boolean {
  return element.scrollHeight - element.scrollTop - element.clientHeight > TAIL_THRESHOLD_PX;
}

function pinToTail(element: HTMLElement) {
  element.scrollTop = Math.max(0, element.scrollHeight - element.clientHeight);
}

function nextTimelineIndex(current: number, key: string, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case "ArrowDown":
      return Math.min(count - 1, current + 1);
    case "ArrowUp":
      return Math.max(0, current - 1);
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}

function unseenLabel(count: number): string {
  return count === 1 ? "1 mensagem nova" : `${count} mensagens novas`;
}

function authorLabel(author: string): string {
  return author.trim().length > 0 ? author : "Autor indisponível";
}

/** Altura 0 significa que o layout ainda não ocorreu. O navegador mede o conteúdo. */
function measureTimelineRow(element: Element): number {
  if (!(element instanceof HTMLElement)) return ESTIMATE_ROW_PX;
  const height = element.offsetHeight;
  return height > 0 ? height : ESTIMATE_ROW_PX;
}

export function MessageTimeline(props: MessageTimelineProps) {
  return <TimelineSession key={conversationScopeKey(props.scope)} {...props} />;
}

function TimelineSession({ scope, protocol, status, entries }: MessageTimelineProps) {
  const parentRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef(new Map<number, HTMLElement>());
  const pendingFocus = useRef<number | null>(null);
  const awayRef = useRef(false);
  const [away, setAway] = useState(false);
  const [frozenCount, setFrozenCount] = useState(0);
  const [focusedIndex, setFocusedIndex] = useState(0);
  const readyEntries = status === "ready" ? entries : [];
  const activeIndex =
    readyEntries.length === 0 ? 0 : Math.min(focusedIndex, readyEntries.length - 1);
  const virtualizer = useVirtualizer({
    count: readyEntries.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ESTIMATE_ROW_PX,
    measureElement: (element) => measureTimelineRow(element),
    overscan: 12,
    initialRect: { width: 640, height: 480 },
    anchorTo: "end",
    followOnAppend: true,
    scrollEndThreshold: TAIL_THRESHOLD_PX,
    getItemKey: (index) => readyEntries[index]?.id ?? index,
    rangeExtractor: (range) => {
      const indexes = defaultRangeExtractor(range);
      if (activeIndex < 0 || activeIndex >= range.count || indexes.includes(activeIndex)) {
        return indexes;
      }
      const next = indexes.slice();
      next.push(activeIndex);
      next.sort((left, right) => left - right);
      return next;
    },
  });
  const unseen = away ? Math.max(0, readyEntries.length - frozenCount) : 0;
  const protocolLabel = protocol.trim().length > 0 ? protocol : "indisponível";

  useLayoutEffect(() => {
    const element = parentRef.current;
    if (!element || away || status !== "ready" || entries.length === 0) return;
    pinToTail(element);
    const last = entries.length - 1;
    let cancelled = false;
    // O evento de scroll disparado aqui cai dentro do layout, e o React
    // ignora flushSync nesse momento. O microtask realinha a janela virtual.
    queueMicrotask(() => {
      if (cancelled || awayRef.current || !parentRef.current) return;
      virtualizer.scrollToIndex(last, { align: "end" });
    });
    return () => {
      cancelled = true;
    };
  }, [away, entries, status, virtualizer]);

  useLayoutEffect(() => {
    const index = pendingFocus.current;
    if (index === null) return;
    const node = itemRefs.current.get(index);
    if (!node) return;
    pendingFocus.current = null;
    if (document.activeElement !== node) node.focus({ preventScroll: true });
  });

  function handleScroll() {
    const element = parentRef.current;
    if (!element) return;
    if (!isAwayFromTail(element)) {
      awayRef.current = false;
      setAway(false);
      return;
    }
    if (!awayRef.current) {
      awayRef.current = true;
      setFrozenCount(readyEntries.length);
    }
    setAway(true);
  }

  function focusEntry(index: number) {
    setFocusedIndex(index);
    pendingFocus.current = index;
    const align = index === 0 ? "start" : index === readyEntries.length - 1 ? "end" : "auto";
    virtualizer.scrollToIndex(index, { align });
    const node = itemRefs.current.get(index);
    if (!node) return;
    pendingFocus.current = null;
    node.focus({ preventScroll: true });
  }

  function jumpToEnd() {
    awayRef.current = false;
    setFrozenCount(readyEntries.length);
    setAway(false);
    const last = readyEntries.length - 1;
    if (last >= 0) focusEntry(last);
    const element = parentRef.current;
    if (element) pinToTail(element);
  }

  function onItemKeyDown(event: KeyboardEvent<HTMLElement>, index: number) {
    const next = nextTimelineIndex(index, event.key, readyEntries.length);
    if (next === null) return;
    event.preventDefault();
    if (next === index) return;
    focusEntry(next);
  }

  return (
    <section className="flex flex-col gap-3" data-scope={conversationScopeKey(scope)}>
      <h2 className="text-base font-medium text-foreground">Protocolo {protocolLabel}</h2>
      <div
        ref={parentRef}
        role="region"
        aria-label="Histórico da conversa"
        aria-busy={status === "loading" ? true : undefined}
        className="max-h-96 overflow-auto rounded-xl ring-1 ring-foreground/10"
        onScroll={handleScroll}
      >
        {status === "loading" ? (
          <p role="status" className="px-4 py-6 text-sm text-foreground">
            Carregando o histórico.
          </p>
        ) : null}
        {status === "error" ? (
          <div role="alert" className="flex flex-col gap-1 px-4 py-6">
            <p className="text-sm font-medium text-foreground">
              Não foi possível carregar o histórico.
            </p>
            <p className="text-sm text-muted-foreground">
              O servidor não respondeu. Nenhuma mensagem foi inventada.
            </p>
          </div>
        ) : null}
        {status === "ready" && readyEntries.length === 0 ? (
          <p className="px-4 py-6 text-sm text-muted-foreground">
            Nenhuma mensagem nesta conversa.
          </p>
        ) : null}
        {status === "ready" && readyEntries.length > 0 ? (
          <ul
            aria-label="Mensagens e eventos"
            className="relative w-full"
            style={{ height: `${virtualizer.getTotalSize()}px` }}
          >
            {virtualizer.getVirtualItems().map((virtualRow) => {
              const entry = readyEntries[virtualRow.index];
              if (!entry) return null;
              return (
                <li
                  key={entry.id}
                  data-index={virtualRow.index}
                  ref={(node) => {
                    virtualizer.measureElement(node);
                    if (node) itemRefs.current.set(virtualRow.index, node);
                    else itemRefs.current.delete(virtualRow.index);
                  }}
                  tabIndex={virtualRow.index === activeIndex ? 0 : -1}
                  aria-posinset={virtualRow.index + 1}
                  aria-setsize={readyEntries.length}
                  aria-label={entryLabel(entry, protocolLabel)}
                  className="absolute top-0 left-0 flex w-full flex-col gap-1 px-4 py-3 text-sm outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                  onFocus={() => setFocusedIndex(virtualRow.index)}
                  onKeyDown={(event) => onItemKeyDown(event, virtualRow.index)}
                >
                  <EntryContent entry={entry} />
                </li>
              );
            })}
          </ul>
        ) : null}
      </div>
      {away ? (
        <div className="flex items-center gap-3">
          {unseen > 0 ? (
            <p role="status" className="text-sm text-foreground">
              {unseenLabel(unseen)}
            </p>
          ) : null}
          <Button type="button" variant="outline" onClick={jumpToEnd}>
            Voltar ao fim
          </Button>
        </div>
      ) : null}
    </section>
  );
}

function entryLabel(entry: TimelineEntry, protocol: string): string {
  const author = authorLabel(entry.author);
  if (entry.kind === "text") {
    const state = entry.localState ? localStateLabel[entry.localState] : "Texto";
    const receipt = entry.receipt ? receiptLabel[entry.receipt] : "";
    return `Texto de ${author}. Protocolo ${protocol}. ${state}. ${receipt} ${entry.text}`.trim();
  }
  if (entry.kind === "event") {
    return `Evento de ${author}. Protocolo ${protocol}. ${entry.summary}`;
  }
  return `Tipo não suportado de ${author}. Protocolo ${protocol}. Referência ${entry.reference}.`;
}

function EntryContent({ entry }: { entry: TimelineEntry }) {
  const author = authorLabel(entry.author);
  return (
    <>
      <p className="text-muted-foreground">
        <span className="font-medium text-foreground">{kindLabel(entry)}</span>
        {" · "}
        <span>{`Autor: ${author}`}</span>
        {entry.occurredAt ? ` · ${formatWhen(entry.occurredAt)}` : null}
      </p>
      {entry.kind === "text" ? (
        <>
          <p className="whitespace-pre-wrap break-words text-foreground">{entry.text}</p>
          <p className="flex flex-wrap gap-2">
            {entry.localState ? (
              <span data-local-state={entry.localState} className={localStateClass[entry.localState]}>
                {localStateLabel[entry.localState]}
              </span>
            ) : null}
            {entry.receipt ? (
              <span data-receipt={entry.receipt} className="text-foreground">
                {receiptLabel[entry.receipt]}
              </span>
            ) : null}
          </p>
        </>
      ) : null}
      {entry.kind === "event" ? (
        <p className="whitespace-pre-wrap break-words text-foreground">{entry.summary}</p>
      ) : null}
      {entry.kind === "unsupported" ? (
        <>
          <p className="text-foreground">Referência: {entry.reference}</p>
          <p className="text-muted-foreground">O painel não interpreta este conteúdo.</p>
        </>
      ) : null}
    </>
  );
}

function kindLabel(entry: TimelineEntry): string {
  if (entry.kind === "text") return "Texto";
  if (entry.kind === "event") return "Evento";
  return "Tipo não suportado";
}
