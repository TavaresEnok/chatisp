import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { createIntentKey } from "@/features/conversation/send-intent";
import type { SendTextCommand } from "@/features/conversation/types";
import {
  CLOSE_RESULT_LABEL,
  CLOSE_RESULTS,
  presentationText,
  readAttendanceResult,
} from "@/features/conversation/workspace-types";
import type { CloseAttendanceFields, CloseResult } from "@/features/conversation/workspace-types";

export type CloseConversationDialogProps = {
  protocol: string;
  createIntentKey?: () => string;
  onClose: (fields: CloseAttendanceFields) => Promise<unknown> | unknown;
  onFarewell: (command: SendTextCommand) => Promise<unknown> | unknown;
  onDismiss: () => void;
};

const fieldClass =
  "min-h-24 w-full rounded-lg border border-input bg-transparent px-2.5 py-2 text-base text-foreground outline-none placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-offset-2 focus-visible:outline-foreground disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30";

function isCloseResult(value: string): value is CloseResult {
  return CLOSE_RESULTS.some((result) => result === value);
}

export function CloseConversationDialog({
  protocol,
  createIntentKey: createKey = createIntentKey,
  onClose,
  onDismiss,
}: CloseConversationDialogProps) {
  const titleId = useId();
  const helpId = useId();
  const reasonId = useId();
  const resultId = useId();
  const synthesisId = useId();
  const farewellId = useId();
  const alive = useRef(true);
  const [reason, setReason] = useState("");
  const [result, setResult] = useState<CloseResult | "">("");
  const [synthesis, setSynthesis] = useState("");
  const [farewell, setFarewell] = useState("");
  const intent = useRef<{ fingerprint: string; close: string; farewell: string } | null>(null);
  const [closeNotice, setCloseNotice] = useState<string | null>(null);
  const [closed, setClosed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const canClose =
    !closed && !busy && reason.trim().length > 0 && result !== "" && synthesis.trim().length > 0;
  const protocolLabel = presentationText(protocol, "indisponível");

  async function submitClose() {
    if (result === "" || reason.trim().length === 0 || synthesis.trim().length === 0 || busy || closed) {
      return;
    }
    setBusy(true);
    setCloseNotice(null);
    const fingerprint = JSON.stringify([reason.trim(), result, synthesis.trim(), farewell.trim()]);
    if (!intent.current || intent.current.fingerprint !== fingerprint) {
      intent.current = { fingerprint, close: createKey(), farewell: farewell.trim() ? createKey() : "" };
    }
    let response: unknown;
    try {
      response = await Promise.resolve(
        onClose({
          reason: reason.trim(),
          result,
          synthesis: synthesis.trim(),
          clientRequestId: intent.current.close,
          ...(farewell.trim() ? { farewellBody: farewell.trim(), farewellRequestId: intent.current.farewell } : {}),
        }),
      );
    } catch {
      response = null;
    }
    if (!alive.current) return;
    const action = readAttendanceResult(response);
    if (action.outcome !== "done") {
      if (action.outcome === "conflict") intent.current = null;
      setUncertain(action.outcome === "lost");
      setCloseNotice(action.detail);
      setBusy(false);
      return;
    }
    setClosed(true);
    setBusy(false);
    if (action.farewellFailed) setCloseNotice("Atendimento encerrado, mas a despedida não foi enfileirada.");
    else onDismiss();
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void submitClose();
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape" && !busy) {
      event.preventDefault();
      onDismiss();
      return;
    }
    if (event.key !== "Tab") return;
    const nodes = [
      ...event.currentTarget.querySelectorAll<HTMLElement>(
        "button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled])",
      ),
    ];
    const first = nodes[0];
    const last = nodes[nodes.length - 1];
    if (!first || !last) return;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      aria-describedby={helpId}
      aria-busy={busy ? true : undefined}
      className="fixed inset-0 z-50 flex items-end justify-center bg-foreground/20 p-4 sm:items-center"
      onKeyDown={onKeyDown}
    >
      <form
        noValidate
        className="flex max-h-[calc(100vh-2rem)] w-full max-w-lg flex-col gap-4 overflow-auto rounded-xl bg-background p-4 text-foreground ring-1 ring-foreground/10"
        onSubmit={onSubmit}
      >
        <div className="flex flex-col gap-1">
          <h2 id={titleId} className="text-base font-medium">
            Encerrar com motivo
          </h2>
          <p className="text-sm text-muted-foreground">{`Protocolo ${protocolLabel}`}</p>
          <p id={helpId} className="text-sm text-muted-foreground">
            Motivo, resultado e síntese são obrigatórios. A despedida é opcional; se falhar,
            o encerramento permanece.
          </p>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={reasonId}>Motivo</Label>
          <Input
            id={reasonId}
            value={reason}
            autoFocus
            autoComplete="off"
            disabled={closed || busy || uncertain}
            aria-required="true"
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={resultId}>Resultado</Label>
          <select
            id={resultId}
            value={result}
            disabled={closed || busy || uncertain}
            aria-required="true"
            className="h-11 w-full rounded-lg border border-input bg-transparent px-2.5 text-base text-foreground outline-none focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-offset-2 focus-visible:outline-foreground disabled:pointer-events-none disabled:opacity-50 md:text-sm dark:bg-input/30"
            onChange={(event) => {
              const next = event.target.value;
              setResult(isCloseResult(next) ? next : "");
            }}
          >
            <option value="">Selecione o resultado</option>
            {CLOSE_RESULTS.map((item) => (
              <option key={item} value={item}>
                {CLOSE_RESULT_LABEL[item]}
              </option>
            ))}
          </select>
          {result === "referred_external" ? (
            <p className="text-sm text-muted-foreground">
              Encerrar esta conversa não encerra a demanda no sistema externo.
            </p>
          ) : null}
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={synthesisId}>Síntese</Label>
          <textarea
            id={synthesisId}
            value={synthesis}
            rows={3}
            disabled={closed || busy || uncertain}
            aria-required="true"
            className={fieldClass}
            onChange={(event) => setSynthesis(event.target.value)}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={farewellId}>Despedida</Label>
          <textarea
            id={farewellId}
            value={farewell}
            rows={3}
            disabled={closed || busy || uncertain}
            aria-required="false"
            placeholder="Opcional"
            className={fieldClass}
            onChange={(event) => setFarewell(event.target.value)}
          />
        </div>
        {closeNotice ? (
          <p role="alert" className="text-sm text-destructive">
            {closeNotice}
          </p>
        ) : null}
        {closed ? (
          <p role="status" className="text-sm text-foreground">
            Atendimento encerrado neste painel.
          </p>
        ) : null}
        <div className="flex flex-wrap gap-2">
          {!closed ? (
            <Button
              type="submit"
              className="h-11 w-fit px-3"
              disabled={!canClose}
            >
              Encerrar
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            className="h-11 w-fit px-3"
            disabled={busy}
            onClick={onDismiss}
          >
            {closed ? "Fechar" : "Cancelar"}
          </Button>
        </div>
      </form>
    </div>
  );
}
