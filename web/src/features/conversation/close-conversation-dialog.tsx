import { useEffect, useId, useReducer, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  createComposerState,
  createIntentKey,
  reduceComposer,
  selectClientRequestId,
} from "@/features/conversation/send-intent";
import type { IntentSubmitMode } from "@/features/conversation/send-intent";
import type { SendTextCommand, SendTextResult } from "@/features/conversation/types";
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
  "min-h-24 w-full rounded-lg border border-input bg-transparent px-2.5 py-2 text-base text-foreground outline-none placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30";

function isCloseResult(value: string): value is CloseResult {
  return CLOSE_RESULTS.some((result) => result === value);
}

function isSendTextResult(value: unknown): value is SendTextResult {
  if (!value || typeof value !== "object" || !("outcome" in value)) return false;
  const outcome = value.outcome;
  return (
    outcome === "accepted" ||
    outcome === "failed" ||
    outcome === "uncertain" ||
    outcome === "lost"
  );
}

/**
 * A despedida usa a mesma decisão de chave do compositor:
 * perda ou incerteza repetem a intenção; não viram entrega nem leitura.
 */
export function CloseConversationDialog({
  protocol,
  createIntentKey: createKey = createIntentKey,
  onClose,
  onFarewell,
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
  const [farewell, dispatchFarewell] = useReducer(reduceComposer, undefined, createComposerState);
  const [closeNotice, setCloseNotice] = useState<string | null>(null);
  const [closed, setClosed] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const canClose =
    !closed && !busy && reason.trim().length > 0 && result !== "" && synthesis.trim().length > 0;
  const canSendFarewell =
    closed &&
    farewell.draft.trim().length > 0 &&
    farewell.phase !== "submitting" &&
    farewell.phase !== "accepted" &&
    !busy;
  const protocolLabel = presentationText(protocol, "indisponível");

  async function sendFarewell(mode: IntentSubmitMode) {
    const intentKey = selectClientRequestId(farewell, mode, createKey);
    if (!intentKey) return;
    const text = farewell.draft;
    dispatchFarewell({ type: "submit", offline: false, intentKey, text });
    let response: SendTextResult;
    try {
      const value = await Promise.resolve(onFarewell({ intentKey, text }));
      response = isSendTextResult(value) ? value : { outcome: "lost" };
    } catch {
      response = { outcome: "lost" };
    }
    if (!alive.current) return;
    dispatchFarewell({ type: "result", intentKey, result: response });
  }

  async function submitClose() {
    if (result === "" || reason.trim().length === 0 || synthesis.trim().length === 0 || busy || closed) {
      return;
    }
    setBusy(true);
    setCloseNotice(null);
    let response: unknown;
    try {
      response = await Promise.resolve(
        onClose({
          reason: reason.trim(),
          result,
          synthesis: synthesis.trim(),
        }),
      );
    } catch {
      response = null;
    }
    if (!alive.current) return;
    const action = readAttendanceResult(response);
    if (action.outcome !== "done") {
      setCloseNotice(action.detail);
      setBusy(false);
      return;
    }
    setClosed(true);
    setBusy(false);
    if (farewell.draft.trim().length === 0) {
      onDismiss();
      return;
    }
    await sendFarewell("reuse-if-pending");
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (closed) {
      void sendFarewell("reuse-if-pending");
      return;
    }
    void submitClose();
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape" && !busy && farewell.phase !== "submitting") {
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
      aria-busy={busy || farewell.phase === "submitting" ? true : undefined}
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
            Motivo, resultado e síntese são obrigatórios. A despedida é opcional e segue como
            outro envio: se ela falhar, o encerramento local permanece.
          </p>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={reasonId}>Motivo</Label>
          <Input
            id={reasonId}
            value={reason}
            autoFocus
            autoComplete="off"
            disabled={closed || busy}
            aria-required="true"
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={resultId}>Resultado</Label>
          <select
            id={resultId}
            value={result}
            disabled={closed || busy}
            aria-required="true"
            className="h-11 w-full rounded-lg border border-input bg-transparent px-2.5 text-base text-foreground outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground disabled:pointer-events-none disabled:opacity-50 md:text-sm dark:bg-input/30"
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
            disabled={closed || busy}
            aria-required="true"
            className={fieldClass}
            onChange={(event) => setSynthesis(event.target.value)}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={farewellId}>Despedida</Label>
          <textarea
            id={farewellId}
            value={farewell.draft}
            rows={3}
            disabled={farewell.phase === "submitting" || busy}
            aria-required="false"
            placeholder="Opcional"
            className={fieldClass}
            onChange={(event) => dispatchFarewell({ type: "edit", draft: event.target.value })}
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
        {farewell.notice ? (
          <p role="status" className="text-sm text-foreground">
            {farewell.notice}
          </p>
        ) : null}
        <div className="flex flex-wrap gap-2">
          {farewell.phase !== "accepted" ? (
            <Button
              type="submit"
              className="h-11 w-fit px-3"
              disabled={closed ? !canSendFarewell : !canClose}
            >
              {closed ? "Enviar despedida" : "Encerrar"}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            className="h-11 w-fit px-3"
            disabled={busy || farewell.phase === "submitting"}
            onClick={onDismiss}
          >
            {closed ? "Fechar" : "Cancelar"}
          </Button>
        </div>
      </form>
    </div>
  );
}
