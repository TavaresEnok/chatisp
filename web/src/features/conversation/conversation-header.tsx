import { Check } from "lucide-react";
import type { ReactNode } from "react";
import { ContactAvatar } from "@/components/ui/contact-avatar";
import { Button } from "@/components/ui/button";
import {
  ATTENDANCE_PHASE_LABEL,
  presentationText,
} from "@/features/conversation/workspace-types";
import type { AttendancePhase } from "@/features/conversation/workspace-types";

export type ConversationHeaderProps = {
  contactLabel: string;
  protocol: string;
  phase: AttendancePhase;
  assigneeLabel: string;
  assuming: boolean;
  notice: string | null;
  onAssume: () => void;
  onClose: () => void;
  /** Controle antes do contato (voltar à fila, em telas estreitas). */
  leading?: ReactNode;
  /** Controle depois das ações (abrir os detalhes, quando o painel não cabe ao lado). */
  trailing?: ReactNode;
};

/** Um e-mail como rótulo ocupa a linha toda: mostra a parte antes do @ e deixa o valor inteiro na dica. */
function shortLabel(label: string): string {
  return /^[^\s@]+@[^\s@]+$/.test(label) ? label.slice(0, label.indexOf("@")) : label;
}

export function ConversationHeader({
  contactLabel,
  protocol,
  phase,
  assigneeLabel,
  assuming,
  notice,
  onAssume,
  onClose,
  leading,
  trailing,
}: ConversationHeaderProps) {
  const contact = presentationText(contactLabel, "Contato indisponível");
  const protocolLabel = presentationText(protocol, "indisponível");
  const assignee = presentationText(assigneeLabel, "Sem responsável");
  const open = phase !== "closed";

  return (
    <header className="conversation-header">
      {leading}
      <div className="conversation-identity">
        <ContactAvatar label={contact} size={36} />
        <div className="conversation-title">
          <h2>{contact}</h2>
          {/* A linha compacta é só visual; as três frases abaixo são o que leitores de tela anunciam. */}
          <p className="conversation-meta" aria-hidden="true">
            <span className="conversation-protocol">{protocolLabel}</span>
            <span className="conversation-phase-mark" data-phase={phase}><i aria-hidden="true" />{ATTENDANCE_PHASE_LABEL[phase]}</span>
            <span className="conversation-assignee" title={assignee}>{shortLabel(assignee)}</span>
          </p>
          <p className="visually-hidden">{`Protocolo ${protocolLabel}`}</p>
          <p className="visually-hidden conversation-phase" data-phase={phase}>{`Estado: ${ATTENDANCE_PHASE_LABEL[phase]}`}</p>
          <p className="visually-hidden">{`Responsável: ${assignee}`}</p>
        </div>
      </div>
      <div className="conversation-actions">
        {open ? (
          <>
            <Button type="button" className="conversation-assume" disabled={assuming} onClick={onAssume}>
              Assumir
            </Button>
            <Button type="button" variant="outline" className="conversation-close" title="Encerrar com motivo" disabled={assuming} onClick={onClose}>
              <Check aria-hidden="true" />
              Encerrar com motivo
            </Button>
          </>
        ) : (
          <p className="conversation-closed">Atendimento encerrado.</p>
        )}
        {trailing}
      </div>
      {assuming ? (
        <p role="status" className="conversation-notice">
          Assumindo a conversa.
        </p>
      ) : null}
      {notice ? (
        <p role="alert" className="conversation-notice">
          {notice}
        </p>
      ) : null}
    </header>
  );
}
