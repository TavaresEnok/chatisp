import { useContext, useEffect, useId, useReducer, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";
import { QueryClientContext } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { cannedScopeToken, denyCannedScope, subscribeCannedDenial } from "@/features/cannedreplies/cache";
import { chooseSlashReply, isCannedDenial, SlashList, SlashReplySource, type SlashResult } from "@/features/cannedreplies/slash-replies";
import { expandedPrefix, insertRendered, repeatsServerPrefix, slashToken } from "@/features/cannedreplies/rules";
import { getSignature, type CannedSignature } from "@/api/cannedreplies";
import { PanelRequestError } from "@/api/problems";
import { ComposerTools } from "@/features/conversation/composer-tools";
import { AudioRecorder } from "@/features/conversation/audio-recorder";
import { draftKey, notifyDraftSettlement, readDraft, subscribeDraftSettlement, writeDraft } from "@/features/conversation/draft-store";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";
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
  SendAudioCommand,
  SendTextCommand,
  SendTextResult,
} from "@/features/conversation/types";

export type MessageComposerProps = {
  scope: ConversationScope;
  offline?: boolean;
  createIntentKey?: () => string;
  localQueue?: boolean;
  onTyping?: () => void;
  replyInboxId?: string;
  onSend: (command: SendTextCommand) => Promise<SendTextResult> | SendTextResult;
  onSendAudio?: (command: SendAudioCommand) => Promise<void> | void;
};

export function MessageComposer(props: MessageComposerProps) {
  const principal = useSessionStore((state) => state.principal);
  const identity = principal && principal.tenantId === props.scope.tenantId && (props.scope.userId === principal.userId || props.scope.userId === `${principal.userId}:${principal.sessionId}`) ? principal : null;
  const storageKey = identity ? draftKey(identity, props.scope) : null;
  return <ComposerSession key={`${conversationScopeKey(props.scope)}:${storageKey ?? ""}`} {...props} storageKey={storageKey} attendant={identity} />;
}

type PrincipalStamp = {
  tenantId: string;
  userId: string;
  sessionId: string;
  membershipId: string;
};

type SignatureKnowledge =
  | { kind: "off" }
  | { kind: "loading" }
  | { kind: "failed" }
  | { kind: "ready"; signature: CannedSignature };

function principalStamp(principal: PanelPrincipal | null): PrincipalStamp | null {
  if (!principal) return null;
  return {
    tenantId: principal.tenantId,
    userId: principal.userId,
    sessionId: principal.sessionId,
    membershipId: principal.membershipId,
  };
}

function samePrincipal(left: PrincipalStamp | null, right: PrincipalStamp | null): boolean {
  if (left === null || right === null) return left === right;
  return left.tenantId === right.tenantId && left.userId === right.userId && left.sessionId === right.sessionId && left.membershipId === right.membershipId;
}

function prefixNotice(prefix: string): string {
  return `O rascunho já começa com o prefixo configurado (${prefix}). O servidor acrescenta esse prefixo de novo na aceitação. Retire o prefixo antes de um novo envio.`;
}

function signatureFailureNotice(): string {
  return "A assinatura não pôde ser consultada. Uma nova aceitação fica bloqueada até a assinatura responder. A falha não confirma a ausência de prefixo.";
}

function isAbortError(error: unknown): boolean {
  return typeof error === "object" && error !== null && "name" in error && (error as { name?: string }).name === "AbortError";
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
  replyInboxId,
  onSendAudio,
  attendant,
}: MessageComposerProps & { storageKey: string | null; attendant: PanelPrincipal | null }) {
  const queryClient = useContext(QueryClientContext);
  const canned = queryClient != null && attendant != null;
  const [state, dispatch] = useReducer(reduceComposer, storageKey, (key) => (key && readDraft(key)) || createComposerState());
  const alive = useRef(true);
  const field = useRef<HTMLTextAreaElement>(null);
  const draftRef = useRef(state.draft);
  const draftRevision = useRef(0);
  if (state.draft !== draftRef.current) {
    draftRef.current = state.draft;
    draftRevision.current += 1;
  }
  const [cursor, setCursor] = useState(0);
  const [slashIndex, setSlashIndex] = useState(0);
  const [slashBusy, setSlashBusy] = useState(false);
  const [slashNotice, setSlashNotice] = useState("");
  const [slashResult, setSlashResult] = useState<SlashResult>({ replies: [], pending: false, failed: false });
  const [prefixHold, setPrefixHold] = useState<string | null>(null);
  const prefixHoldRef = useRef<string | null>(null);
  const [signatureUnconfirmed, setSignatureUnconfirmed] = useState(() => canned && state.draft.trim().length > 0);
  const signatureUnconfirmedRef = useRef(signatureUnconfirmed);
  const signatureEpoch = useRef(0);
  const signatureAbort = useRef<AbortController | null>(null);
  const cannedDenied = useRef(false);
  const signatureRef = useRef<SignatureKnowledge>({ kind: canned ? "loading" : "off" });
  if (canned && signatureRef.current.kind === "off") signatureRef.current = { kind: "loading" };
  if (!canned) signatureRef.current = { kind: "off" };
  const [dismissedToken, setDismissedToken] = useState<string | null>(null);
  const fieldId = useId();
  const slashListId = useId();
  const helpId = useId();
  const noticeId = useId();
  const describedBy = state.notice ? `${helpId} ${noticeId}` : helpId;
  const token = slashToken(state.draft, cursor);
  const tokenKey = token ? `${token.start}:${token.query}` : "";
  const slashOpen = token !== null && dismissedToken !== tokenKey && state.phase !== "submitting" && canned;
  const boundedSlash = slashResult.replies.length === 0 ? 0 : Math.min(slashIndex, slashResult.replies.length - 1);
  const activeSlash = slashOpen ? slashResult.replies[boundedSlash] : undefined;
  const activeSlashId = activeSlash ? `${slashListId}-${activeSlash.id}` : undefined;
  const prefixBlocksDraft = prefixHold !== null && state.draft.startsWith(prefixHold);
  const signatureBlocksNew = signatureUnconfirmed && state.draft.trim().length > 0;
  const canStartNewDecision = state.draft.trim().length > 0 && state.phase !== "submitting" && state.pending !== null && !prefixBlocksDraft && !signatureBlocksNew;
  const canSubmit = state.phase !== "submitting" && (state.pending !== null || (state.draft.trim().length > 0 && !prefixBlocksDraft && !signatureBlocksNew));

  function setHold(value: string | null) {
    prefixHoldRef.current = value;
    setPrefixHold(value);
  }

  function setSignatureGate(blocked: boolean) {
    signatureUnconfirmedRef.current = blocked;
    setSignatureUnconfirmed(blocked);
  }

  function stopSend(captured: PrincipalStamp | null, epoch: number): boolean {
    return !alive.current || epoch !== signatureEpoch.current || !samePrincipal(captured, principalStamp(useSessionStore.getState().principal));
  }

  function applyKnownSignature(draft: string, signature: CannedSignature, email: string) {
    if (draft.trim().length === 0) {
      setHold(null);
      setSignatureGate(false);
      return;
    }
    if (repeatsServerPrefix(draft, signature, email)) {
      const prefix = expandedPrefix(signature.prefix, email);
      setHold(prefix);
      setSignatureGate(false);
      setSlashNotice(prefixNotice(prefix));
      return;
    }
    if (prefixHoldRef.current) {
      setHold(null);
      setSlashNotice("");
    }
    setSignatureGate(false);
  }

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

  function revokeComposerCanned(message: string) {
    if (cannedDenied.current) return;
    cannedDenied.current = true;
    signatureEpoch.current += 1;
    signatureAbort.current?.abort();
    signatureRef.current = { kind: "failed" };
    if (!alive.current) return;
    setSlashResult({ replies: [], pending: false, failed: true });
    setSlashNotice(message);
    setSlashBusy(false);
    if (draftRef.current.trim().length > 0) setSignatureGate(true);
  }

  function rejectCanned(error: unknown): boolean {
    if (!isCannedDenial(error)) return false;
    if (!attendant || !queryClient || cannedDenied.current) return true;
    denyCannedScope(queryClient, attendant, error.message);
    revokeComposerCanned(error.message);
    return true;
  }

  useEffect(() => {
    if (!attendant) return;
    return subscribeCannedDenial((token, message) => {
      if (token !== cannedScopeToken(attendant)) return;
      revokeComposerCanned(message);
    });
  }, [attendant]);

  useEffect(() => {
    if (!canned || !attendant) return;
    let cancelled = false;
    const epoch = signatureEpoch.current + 1;
    signatureEpoch.current = epoch;
    const captured = principalStamp(attendant);
    const controller = new AbortController();
    signatureAbort.current = controller;
    if (draftRef.current.trim().length > 0) setSignatureGate(true);
    void getSignature(controller.signal).then((signature) => {
      if (cancelled || !alive.current || cannedDenied.current || epoch !== signatureEpoch.current) return;
      if (!samePrincipal(captured, principalStamp(useSessionStore.getState().principal))) return;
      signatureRef.current = { kind: "ready", signature };
      applyKnownSignature(draftRef.current, signature, attendant.email);
    }).catch((error: unknown) => {
      if (isAbortError(error)) return;
      if (!samePrincipal(captured, principalStamp(useSessionStore.getState().principal))) return;
      if (rejectCanned(error)) return;
      if (cancelled || !alive.current || epoch !== signatureEpoch.current) return;
      signatureRef.current = { kind: "failed" };
      if (draftRef.current.trim().length > 0) {
        setSignatureGate(true);
        setSlashNotice(signatureFailureNotice());
      }
    });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [attendant, canned]);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      signatureEpoch.current += 1;
      signatureAbort.current?.abort();
    };
  }, []);

  function changeDraft(draft: string) {
    if (draft !== draftRef.current) {
      draftRef.current = draft;
      draftRevision.current += 1;
    }
    dispatch({ type: "edit", draft });
    const known = signatureRef.current;
    if (known.kind === "ready" && attendant && !signatureUnconfirmedRef.current) {
      applyKnownSignature(draft, known.signature, attendant.email);
      return;
    }
    if (prefixHoldRef.current && !draft.startsWith(prefixHoldRef.current)) setHold(null);
    if (draft.trim().length === 0) {
      setSignatureGate(false);
      return;
    }
    if (known.kind === "failed" || signatureUnconfirmedRef.current) setSignatureGate(true);
  }

  function removeRepeatedPrefix() {
    const held = prefixHoldRef.current;
    if (!held || !draftRef.current.startsWith(held) || state.phase === "submitting") return;
    const rest = draftRef.current.slice(held.length);
    changeDraft(rest);
    if (rest.startsWith(held)) {
      setHold(held);
      setSlashNotice("Ainda há um prefixo repetido no início. Retire de novo antes do envio.");
      return;
    }
    setHold(null);
    setSlashNotice("O prefixo repetido foi retirado a seu pedido. O restante do texto permanece no rascunho.");
  }

  async function send(mode: IntentSubmitMode) {
    if (state.phase === "submitting") return;
    // Intenção pendente (perdida/incerta/offline) é repetida com a mesma chave e o mesmo texto;
    // o rascunho editado só vira outra intenção por decisão explícita.
    const replay = mode === "reuse-if-pending" ? state.pending : null;
    const captured = principalStamp(useSessionStore.getState().principal);
    const snapshot = draftRef.current;
    let requestEpoch = signatureEpoch.current;
    const knowledge = signatureRef.current;
    const confirmedSignature = knowledge.kind === "ready" && !signatureUnconfirmedRef.current;
    if (!replay && knowledge.kind !== "off") {
      const email = attendant?.email ?? "";
      if (confirmedSignature && knowledge.kind === "ready") {
        if (stopSend(captured, requestEpoch)) return;
        if (repeatsServerPrefix(snapshot, knowledge.signature, email)) {
          const prefix = expandedPrefix(knowledge.signature.prefix, email);
          setHold(prefix);
          setSlashNotice(prefixNotice(prefix));
          return;
        }
        if (prefixHoldRef.current) setHold(null);
      } else {
        requestEpoch = signatureEpoch.current + 1;
        signatureEpoch.current = requestEpoch;
        const controller = new AbortController();
        signatureAbort.current?.abort();
        signatureAbort.current = controller;
        try {
          const signature = await getSignature(controller.signal);
          if (stopSend(captured, requestEpoch)) return;
          if (draftRef.current !== snapshot) {
            setSlashNotice("O rascunho mudou durante a consulta da assinatura. O envio não foi feito.");
            return;
          }
          signatureRef.current = { kind: "ready", signature };
          setSignatureGate(false);
          if (repeatsServerPrefix(snapshot, signature, email)) {
            const prefix = expandedPrefix(signature.prefix, email);
            setHold(prefix);
            setSlashNotice(prefixNotice(prefix));
            return;
          }
          setHold(null);
        } catch (error) {
          if (isAbortError(error)) return;
          if (!samePrincipal(captured, principalStamp(useSessionStore.getState().principal))) return;
          if (rejectCanned(error)) return;
          if (stopSend(captured, requestEpoch)) return;
          if (draftRef.current !== snapshot) {
            setSlashNotice("O rascunho mudou durante a consulta da assinatura. O envio não foi feito.");
            return;
          }
          signatureRef.current = { kind: "failed" };
          setSignatureGate(true);
          setSlashNotice(signatureFailureNotice());
          return;
        }
      }
    }
    if (stopSend(captured, signatureEpoch.current)) return;
    if (!replay && prefixHoldRef.current && draftRef.current.startsWith(prefixHoldRef.current)) {
      setSlashNotice(prefixNotice(prefixHoldRef.current));
      return;
    }
    if (!replay && draftRef.current !== snapshot) {
      setSlashNotice("O rascunho mudou durante a consulta da assinatura. O envio não foi feito.");
      return;
    }
    const intentKey = replay ? replay.intentKey : selectClientRequestId(state, mode, createKey);
    if (!intentKey) return;
    const text = replay ? replay.text : snapshot;
    const typed = draftRef.current;
    if (stopSend(captured, signatureEpoch.current)) return;
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
    if (slashOpen) return;
    void send("reuse-if-pending");
  }

  function rememberCursor(node: HTMLTextAreaElement | null) {
    if (!node) return;
    setCursor(node.selectionStart ?? node.value.length);
  }

  async function insertSlash(index = boundedSlash) {
    const reply = slashResult.replies[index];
    if (!reply || !token || slashBusy || !replyInboxId) {
      if (!replyInboxId) setSlashNotice("A caixa desta conversa não está disponível para renderizar.");
      return;
    }
    const seen = draftRevision.current;
    const snapshot = draftRef.current;
    const tokenStart = token.start;
    const tokenCursor = cursor;
    const captured = principalStamp(useSessionStore.getState().principal);
    const epoch = signatureEpoch.current + 1;
    signatureEpoch.current = epoch;
    const controller = new AbortController();
    signatureAbort.current?.abort();
    signatureAbort.current = controller;
    setSlashBusy(true);
    setSlashNotice("");
    try {
      const inserted = await chooseSlashReply(reply, replyInboxId, scope.conversationId, controller.signal, (error) => {
        rejectCanned(error);
      });
      if (!alive.current || cannedDenied.current || epoch !== signatureEpoch.current || !samePrincipal(captured, principalStamp(useSessionStore.getState().principal))) return;
      const freshSignature = inserted.signatureKnown && inserted.signature ? inserted.signature : null;
      if (freshSignature) {
        signatureRef.current = { kind: "ready", signature: freshSignature };
        setSignatureGate(false);
      } else {
        signatureRef.current = { kind: "failed" };
        if (draftRef.current.trim().length > 0) setSignatureGate(true);
      }
      if (draftRevision.current !== seen || draftRef.current !== snapshot) {
        if (freshSignature && attendant) applyKnownSignature(draftRef.current, freshSignature, attendant.email);
        setSlashNotice("A resposta chegou depois de uma edição do rascunho e não foi aplicada.");
        return;
      }
      const next = insertRendered(snapshot, tokenStart, tokenCursor, inserted.text);
      if (!freshSignature) setSignatureGate(next.trim().length > 0);
      changeDraft(next);
      const place = tokenStart + inserted.text.length;
      const missing = inserted.missing.filter((item) => item.length > 0);
      const prefix = inserted.signatureKnown && inserted.signature?.enabled ? expandedPrefix(inserted.signature.prefix, attendant?.email ?? "") : "";
      const duplicated = inserted.signatureKnown && repeatsServerPrefix(next, inserted.signature, attendant?.email ?? "");
      setSignatureGate(!inserted.signatureKnown && next.trim().length > 0);
      setHold(duplicated ? prefix : null);
      const notes = [
        missing.length > 0 ? `Marcadores sem valor: ${missing.join(", ")}.` : "",
        !inserted.signatureKnown ? "A assinatura não pôde ser consultada. O texto renderizado entrou no rascunho, mas uma nova aceitação fica bloqueada. A falha não confirma a ausência de prefixo." : "",
        duplicated ? `O rascunho já começa com o prefixo configurado (${prefix}). O servidor acrescenta esse prefixo de novo na aceitação. Retire o prefixo antes de um novo envio.` : "",
        inserted.signatureKnown && !duplicated ? "O texto renderizado entrou no rascunho. A assinatura, se estiver ligada, entra só na aceitação." : "",
      ].filter((item) => item.length > 0);
      setSlashNotice(notes.join(" "));
      setSlashIndex(0);
      requestAnimationFrame(() => {
        field.current?.focus();
        field.current?.setSelectionRange(place, place);
        setCursor(place);
      });
    } catch (error) {
      if (isAbortError(error) || !alive.current) return;
      if (!samePrincipal(captured, principalStamp(useSessionStore.getState().principal))) return;
      if (rejectCanned(error)) return;
      if (epoch !== signatureEpoch.current) return;
      setSlashNotice(error instanceof PanelRequestError ? error.message : "Não foi possível renderizar a resposta.");
    } finally {
      if (alive.current && epoch === signatureEpoch.current) setSlashBusy(false);
    }
  }

  function onComposerKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (slashOpen && !event.nativeEvent.isComposing) {
      if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Home" || event.key === "End") {
        event.preventDefault();
        const count = slashResult.replies.length;
        if (count === 0) return;
        if (event.key === "Home") setSlashIndex(0);
        else if (event.key === "End") setSlashIndex(count - 1);
        else if (event.key === "ArrowDown") setSlashIndex((index) => Math.min(count - 1, index + 1));
        else setSlashIndex((index) => Math.max(0, index - 1));
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setDismissedToken(tokenKey);
        return;
      }
      if (event.key === "Enter") {
        event.preventDefault();
        void insertSlash();
        return;
      }
    }
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.nativeEvent.isComposing) {
      event.preventDefault();
      void send("reuse-if-pending");
    }
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
          aria-expanded={slashOpen}
          aria-controls={slashOpen ? slashListId : undefined}
          aria-owns={slashOpen ? slashListId : undefined}
          aria-autocomplete={slashOpen ? "list" : undefined}
          aria-activedescendant={activeSlashId}
          onChange={(event) => { changeDraft(event.target.value); setCursor(event.target.selectionStart ?? event.target.value.length); onTyping?.(); }}
          onKeyUp={(event) => rememberCursor(event.currentTarget)}
          onClick={(event) => rememberCursor(event.currentTarget)}
          onSelect={(event) => rememberCursor(event.currentTarget)}
          onKeyDown={onComposerKey}
        />
        {canned && attendant ? (
          <SlashReplySource principal={attendant} open={slashOpen} filter={token?.query ?? ""} onResult={setSlashResult} />
        ) : null}
        {canned && attendant ? (
          <SlashList
            open={slashOpen}
            replies={slashResult.replies}
            pending={slashResult.pending}
            failed={slashResult.failed}
            activeIndex={boundedSlash}
            listId={slashListId}
            busy={slashBusy}
            notice=""
            onActiveIndex={setSlashIndex}
            onChoose={(reply) => {
              const index = slashResult.replies.findIndex((item) => item.id === reply.id);
              void insertSlash(index < 0 ? boundedSlash : index);
            }}
          />
        ) : null}
        {slashNotice ? <p>{slashNotice}</p> : null}
        {prefixBlocksDraft ? <button type="button" onClick={removeRepeatedPrefix}>Retirar prefixo repetido</button> : null}
        <ComposerTools field={field} value={state.draft} onChange={changeDraft} />
        <AudioRecorder
          disabled={offline || state.phase === "submitting"}
          onSend={
            onSendAudio
              ? async (capture) => {
                  await onSendAudio({ intentKey: createKey(), ...capture });
                }
              : undefined
          }
        />
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
