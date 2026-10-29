import { useEffect, useId, useReducer, useRef } from "react";
import type { FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  conversationScopeKey,
  createComposerState,
  createIntentKey,
  reduceComposer,
  selectClientRequestId,
} from "@/features/conversation/send-intent";
import type { IntentSubmitMode } from "@/features/conversation/send-intent";
import type {
  ConversationScope,
  SendTextCommand,
  SendTextResult,
} from "@/features/conversation/types";

export type MessageComposerProps = {
  scope: ConversationScope;
  offline?: boolean;
  createIntentKey?: () => string;
  onSend: (command: SendTextCommand) => Promise<SendTextResult> | SendTextResult;
};

export function MessageComposer(props: MessageComposerProps) {
  return <ComposerSession key={conversationScopeKey(props.scope)} {...props} />;
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

function ComposerSession({
  scope,
  offline = false,
  createIntentKey: createKey = createIntentKey,
  onSend,
}: MessageComposerProps) {
  const [state, dispatch] = useReducer(reduceComposer, undefined, createComposerState);
  const alive = useRef(true);
  const fieldId = useId();
  const helpId = useId();
  const noticeId = useId();
  const describedBy = state.notice ? `${helpId} ${noticeId}` : helpId;
  const canSubmit = state.draft.trim().length > 0 && state.phase !== "submitting";
  const canStartNewDecision = canSubmit && state.pending !== null;

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  async function send(mode: IntentSubmitMode) {
    const intentKey = selectClientRequestId(state, mode, createKey);
    if (!intentKey) return;
    const text = state.draft;
    dispatch({ type: "submit", offline, intentKey, text });
    if (offline) return;
    try {
      const result = await Promise.resolve(onSend({ intentKey, text }));
      if (!alive.current) return;
      if (!isSendTextResult(result)) {
        dispatch({ type: "result", intentKey, result: { outcome: "lost" } });
        return;
      }
      dispatch({ type: "result", intentKey, result });
    } catch {
      if (!alive.current) return;
      dispatch({ type: "result", intentKey, result: { outcome: "lost" } });
    }
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void send("reuse-if-pending");
  }

  return (
    <form
      aria-label="Compositor de mensagem"
      className="flex flex-col gap-3"
      noValidate
      data-scope={conversationScopeKey(scope)}
      onSubmit={onSubmit}
    >
      <div className="flex flex-col gap-2">
        <Label htmlFor={fieldId}>Mensagem</Label>
        <textarea
          id={fieldId}
          value={state.draft}
          rows={3}
          autoComplete="off"
          aria-invalid={state.phase === "failed" ? true : undefined}
          aria-describedby={describedBy}
          placeholder="Escreva a mensagem"
          className="min-h-24 w-full rounded-lg border border-input bg-transparent px-2.5 py-2 text-base text-foreground outline-none placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30"
          onChange={(event) => dispatch({ type: "edit", draft: event.target.value })}
        />
        <p id={helpId} className="text-sm text-muted-foreground">
          O rascunho fica só nesta conversa, nesta aba. O conector ainda não o confirmou.
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" className="h-11 w-fit px-3" disabled={!canSubmit}>
          Enviar
        </Button>
        {canStartNewDecision ? (
          <Button
            type="button"
            variant="outline"
            className="h-11 w-fit px-3"
            onClick={() => void send("new-decision")}
          >
            Enviar como nova mensagem
          </Button>
        ) : null}
      </div>
      {state.notice ? (
        <p id={noticeId} role="status" className="text-sm text-foreground">
          {state.notice}
        </p>
      ) : null}
    </form>
  );
}
