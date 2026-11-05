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
};

export function ConversationHeader({
  contactLabel,
  protocol,
  phase,
  assigneeLabel,
  assuming,
  notice,
  onAssume,
  onClose,
}: ConversationHeaderProps) {
  const contact = presentationText(contactLabel, "Contato indisponível");
  const protocolLabel = presentationText(protocol, "indisponível");
  const assignee = presentationText(assigneeLabel, "Sem responsável");
  const open = phase !== "closed";

  return (
    <header className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2">
          <ContactAvatar label={contact} size={40} />
          <h2 className="text-base font-medium text-foreground">{contact}</h2>
        </div>
        <p className="text-sm text-foreground">{`Protocolo ${protocolLabel}`}</p>
        <p className="text-sm text-muted-foreground">{`Estado: ${ATTENDANCE_PHASE_LABEL[phase]}`}</p>
        <p className="text-sm text-muted-foreground">{`Responsável: ${assignee}`}</p>
      </div>
      {open ? (
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            className="h-11 w-fit px-3"
            disabled={assuming}
            onClick={onAssume}
          >
            Assumir
          </Button>
          <Button
            type="button"
            variant="outline"
            className="h-11 w-fit px-3"
            disabled={assuming}
            onClick={onClose}
          >
            Encerrar com motivo
          </Button>
        </div>
      ) : (
        <p className="text-sm text-foreground">Atendimento encerrado.</p>
      )}
      {assuming ? (
        <p role="status" className="text-sm text-foreground">
          Assumindo a conversa.
        </p>
      ) : null}
      {notice ? (
        <p role="alert" className="text-sm text-destructive">
          {notice}
        </p>
      ) : null}
    </header>
  );
}
