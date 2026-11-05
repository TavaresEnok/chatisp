import { ContactAvatar } from "@/components/ui/contact-avatar";
import { useId } from "react";
import type { KeyboardEvent } from "react";
import {
  ATTENDANCE_PHASE_LABEL,
  presentationText,
} from "@/features/conversation/workspace-types";
import type {
  ConversationCard,
  ConversationListStatus,
} from "@/features/conversation/workspace-types";

export type ConversationListProps = {
  status: ConversationListStatus;
  conversations: readonly ConversationCard[];
  selectedId: string | null;
  onSelect: (conversationId: string) => void;
};

function nextIndex(current: number, key: string, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case "ArrowDown":
      return current < 0 ? 0 : Math.min(count - 1, current + 1);
    case "ArrowUp":
      return current < 0 ? count - 1 : Math.max(0, current - 1);
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}

function optionLabel(card: ConversationCard): string {
  const protocol = presentationText(card.protocol, "indisponível");
  const contact = presentationText(card.contactLabel, "Contato indisponível");
  const assignee = presentationText(card.assigneeLabel, "Sem responsável");
  return `Protocolo ${protocol}. ${contact}. ${ATTENDANCE_PHASE_LABEL[card.phase]}. Responsável: ${assignee}.`;
}

export function ConversationList({
  status,
  conversations,
  selectedId,
  onSelect,
}: ConversationListProps) {
  const baseId = useId();

  function onOptionKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    const next = nextIndex(index, event.key, conversations.length);
    if (next === null) return;
    event.preventDefault();
    const card = conversations[next];
    if (!card || card.id === selectedId) return;
    onSelect(card.id);
    const option = document.getElementById(`${baseId}-${next}`);
    if (option instanceof HTMLElement) option.focus();
  }

  return (
    <section className="flex min-w-0 flex-col gap-3" aria-labelledby={`${baseId}-titulo`}>
      <h2 id={`${baseId}-titulo`} className="text-base font-medium text-foreground">
        Conversas
      </h2>
      {status === "loading" ? (
        <p role="status" aria-busy="true" className="text-sm text-foreground">
          Carregando as conversas.
        </p>
      ) : null}
      {status === "error" ? (
        <div role="alert" className="flex flex-col gap-1">
          <p className="text-sm font-medium text-foreground">Não foi possível carregar as conversas.</p>
          <p className="text-sm text-muted-foreground">
            O servidor não respondeu. Nenhuma conversa foi inventada.
          </p>
        </div>
      ) : null}
      {status === "ready" && conversations.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nenhuma conversa nesta caixa.</p>
      ) : null}
      {status === "ready" && conversations.length > 0 ? (
        <div
          role="listbox"
          aria-label="Conversas"
          aria-activedescendant={
            selectedId === null
              ? undefined
              : `${baseId}-${conversations.findIndex((item) => item.id === selectedId)}`
          }
          className="flex max-h-80 flex-col gap-1 overflow-auto rounded-xl ring-1 ring-foreground/10 lg:max-h-[calc(100vh-8rem)]"
        >
          {conversations.map((card, index) => {
            const selected = card.id === selectedId;
            return (
              <button
                key={card.id}
                id={`${baseId}-${index}`}
                type="button"
                role="option"
                aria-selected={selected}
                aria-label={optionLabel(card)}
                tabIndex={selected || (selectedId === null && index === 0) ? 0 : -1}
                className="flex min-h-11 w-full flex-col items-start gap-0.5 px-4 py-2 text-left text-sm outline-none focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-offset-2 focus-visible:outline-foreground aria-selected:bg-muted"
                onClick={() => onSelect(card.id)}
                onKeyDown={(event) => onOptionKeyDown(event, index)}
              >
                <span className="flex items-center gap-2">
                  <ContactAvatar label={presentationText(card.contactLabel, "Contato indisponível")} />
                  <span className="font-medium text-foreground">
                    {presentationText(card.contactLabel, "Contato indisponível")}
                  </span>
                </span>
                <span className="text-muted-foreground">
                  {`Protocolo ${presentationText(card.protocol, "indisponível")} · ${ATTENDANCE_PHASE_LABEL[card.phase]}`}
                </span>
              </button>
            );
          })}
        </div>
      ) : null}
    </section>
  );
}
