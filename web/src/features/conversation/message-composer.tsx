import { useEffect, useId, useReducer, useRef } from "react";
import type { FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { ComposerTools } from "@/features/conversation/composer-tools";
import { draftKey, notifyDraftSettlement, readDraft, subscribeDraftSettlement, writeDraft } from "@/features/conversation/draft-store";
import { useSessionStore } from "@/stores/session-store";
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
  localQueue?: boolean;
  onTyping?: () => void;
  onSend: (command: SendTextCommand) => Promise<SendTextResult> | SendTextResult;
};

export function MessageComposer(props: MessageComposerProps) {
  const principal = useSessionStore((state) => state.principal);
  const identity = principal && principal.tenantId === props.scope.tenantId && (props.scope.userId === principal.userId || props.scope.userId === `${principal.userId}:${principal.sessionId}`) ? principal : null;
  const storageKey = identity ? draftKey(identity, props.scope) : null;
  return <ComposerSession key={`${conversationScopeKey(props.scope)}:${storageKey ?? ""}`} {...props} storageKey={storageKey} />;
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
  localQueue = false,
  storageKey,
  onTyping,
}: MessageComposerProps & { storageKey: string | null }) {
  const [state, dispatch] = useReducer(reduceComposer, storageKey, (key) => (key && readDraft(key)) || createComposerState());
  const alive = useRef(true);
  const field = useRef<HTMLTextAreaElement>(null);
  const fieldId = useId();
  const helpId = useId();
  const noticeId = useId();
  const describedBy = state.notice ? `${helpId} ${noticeId}` : helpId;
  const canStartNewDecision = state.draft.trim().length > 0 && state.phase !== "submitting" && state.pending !== null;
  const canSubmit = state.phase !== "submitting" && (state.pending !== null || state.draft.trim().length > 0);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    if (!storageKey) return;
    return subscribeDraftSettlement(storageKey, ({ intentKey, result }) => {
      dispatch({ type: "result", intentKey, result });
    });
  }, [storageKey]);

  useEffect(() => {
    const principal = useSessionStore.getState().principal;
    if (storageKey && principal && draftKey(principal, scope) === storageKey) writeDraft(storageKey, state);
  }, [storageKey, state, scope]);

  async function send(mode: IntentSubmitMode) {
    if (state.phase === "submitting") return;
    // Intenção pendente (perdida/incerta/offline) é repetida com a mesma chave e o mesmo texto;
    // o rascunho editado só vira outra intenção por decisão explícita.
    const replay = mode === "reuse-if-pending" ? state.pending : null;
    const intentKey = replay ? replay.intentKey : selectClientRequestId(state, mode, createKey);
    if (!intentKey) return;
    const text = replay ? replay.text : state.draft;
    const typed = state.draft;
    dispatch({ type: "submit", offline, intentKey, text });
    // O reducer fixa o rascunho no texto enviado; restaura o que foi digitado.
    if (typed !== text) dispatch({ type: "edit", draft: typed });
    if (offline) return;
    try {
      const result = await Promise.resolve(onSend({ intentKey, text }));
      settle(intentKey, isSendTextResult(result) ? result : { outcome: "lost" });
    } catch {
      settle(intentKey, { outcome: "lost" });
    }
  }

  function settle(intentKey: string, result: SendTextResult) {
    if (alive.current) {
      dispatch({ type: "result", intentKey, result });
      return;
    }
    // Instância desmontada: concilia o armazenamento se a sessão ainda for a mesma.
    if (!storageKey) return;
    const current = readDraft(storageKey);
    const principal = useSessionStore.getState().principal;
    if (!current || !principal || draftKey(principal, scope) !== storageKey) return;
    writeDraft(storageKey, reduceComposer(current, { type: "result", intentKey, result }));
    notifyDraftSettlement(storageKey, { intentKey, result });
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
          ref={field}
          id={fieldId}
          value={state.draft}
          rows={3}
          autoComplete="off"
          aria-invalid={state.phase === "failed" ? true : undefined}
          aria-describedby={describedBy}
          placeholder="Escreva a mensagem"
          className="min-h-24 w-full rounded-lg border border-input bg-transparent px-2.5 py-2 text-base text-foreground outline-none placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30"
          onChange={(event) => { dispatch({ type: "edit", draft: event.target.value }); onTyping?.(); }}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.nativeEvent.isComposing) {
              event.preventDefault();
              void send("reuse-if-pending");
            }
          }}
        />
        <ComposerTools field={field} value={state.draft} onChange={(draft) => dispatch({ type: "edit", draft })} />
        <p id={helpId} className="text-sm text-muted-foreground">
          O rascunho fica neste dispositivo. Ctrl+Enter envia; o conector ainda não confirmou.
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
          {localQueue && state.phase === "accepted" ? "Mensagem enfileirada localmente; a entrega ainda não foi confirmada." : state.notice}
        </p>
      ) : null}
    </form>
  );
}
