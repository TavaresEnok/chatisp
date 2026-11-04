import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getSignature, listReplies, renderReply, type CannedReply, type CannedSignature } from "@/api/cannedreplies";
import { PanelRequestError } from "@/api/problems";
import { cannedKey, cannedScopeToken, denyCannedScope, subscribeCannedDenial } from "@/features/cannedreplies/cache";
import type { PanelPrincipal } from "@/stores/session-store";

function invalidated(): DOMException {
  return new DOMException("A consulta foi invalidada.", "AbortError");
}

export type SlashInsert = {
  text: string;
  missing: string[];
  signature: CannedSignature | null;
  signatureKnown: boolean;
};

export function useSlashReplies(principal: PanelPrincipal | null, open: boolean, filter: string) {
  const client = useQueryClient();
  const revokedRef = useRef(false);
  const generation = useRef(0);
  const [revoked, setRevoked] = useState(false);
  const key = principal ? cannedKey(principal, "slash", filter) : (["m4a-canned", "signed-out", filter] as const);
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      if (revokedRef.current) throw invalidated();
      const stamp = generation.current;
      const page = await listReplies(filter, 25, 0, signal);
      if (revokedRef.current || stamp !== generation.current || signal.aborted) throw invalidated();
      return page;
    },
    enabled: open && principal !== null && !revoked,
    retry: false,
  });
  useEffect(() => {
    if (!principal) return;
    return subscribeCannedDenial((token) => {
      if (token !== cannedScopeToken(principal)) return;
      revokedRef.current = true;
      generation.current += 1;
      setRevoked(true);
    });
  }, [principal]);
  useEffect(() => () => {
    generation.current += 1;
  }, []);
  useEffect(() => {
    if (principal && query.error instanceof PanelRequestError && [403, 404].includes(query.error.status)) {
      denyCannedScope(client, principal, query.error.message);
    }
  }, [client, principal, query.error]);
  const blocked = revoked;
  const replies = blocked ? [] : (query.data?.replies ?? []).filter((reply) => reply.shortcode.startsWith(filter));
  return { replies, pending: open && !blocked && query.isPending, failed: blocked || query.isError };
}

export type SlashResult = { replies: readonly CannedReply[]; pending: boolean; failed: boolean };

/** Só monta onde já existe QueryClientProvider. O compositor legado não entra aqui. */
export function SlashReplySource({
  principal,
  open,
  filter,
  onResult,
}: {
  principal: PanelPrincipal;
  open: boolean;
  filter: string;
  onResult: (result: SlashResult) => void;
}) {
  const slash = useSlashReplies(principal, open, filter);
  const onResultRef = useRef(onResult);
  const marker = `${slash.pending ? 1 : 0}:${slash.failed ? 1 : 0}:${slash.replies.map((reply) => `${reply.id}:${reply.shortcode}:${reply.title}`).join("\n")}`;
  const snapshot = useRef({ replies: slash.replies, pending: slash.pending, failed: slash.failed });
  useEffect(() => {
    onResultRef.current = onResult;
    snapshot.current = { replies: slash.replies, pending: slash.pending, failed: slash.failed };
  });
  useEffect(() => {
    onResultRef.current(snapshot.current);
  }, [marker]);
  return null;
}

function aborted(error: unknown): boolean {
  return typeof error === "object" && error !== null && "name" in error && (error as { name?: string }).name === "AbortError";
}

export function isCannedDenial(error: unknown): error is PanelRequestError {
  return error instanceof PanelRequestError && [403, 404].includes(error.status);
}

export async function chooseSlashReply(
  reply: CannedReply,
  inboxId: string,
  conversationId: string,
  signal?: AbortSignal,
  onDenial?: (error: PanelRequestError) => void,
): Promise<SlashInsert> {
  // Promise.all só entrega a primeira rejeição. A negação do outro pedido
  // continua observada e segue para onDenial mesmo depois disso.
  let pending = 2;
  let denied = false;
  let resolveDenial: (error: PanelRequestError | null) => void = () => undefined;
  const denial = new Promise<PanelRequestError | null>((resolve) => {
    resolveDenial = resolve;
  });
  const noteDenial = (error: unknown) => {
    if (denied || !isCannedDenial(error)) return;
    denied = true;
    resolveDenial(error);
  };
  const finished = () => {
    pending -= 1;
    if (!denied && pending <= 0) resolveDenial(null);
  };
  const watch = (promise: Promise<unknown>) => {
    promise.then(
      () => finished(),
      (error: unknown) => {
        noteDenial(error);
        finished();
      },
    );
  };
  const draftPromise = renderReply(reply.id, inboxId, conversationId, signal);
  const signaturePromise = getSignature(signal).then(
    (signature) => ({ signature, signatureKnown: true as const }),
    (error: unknown) => {
      if (signal?.aborted || aborted(error) || isCannedDenial(error)) throw error;
      return { signature: null, signatureKnown: false as const };
    },
  );
  watch(draftPromise);
  watch(signaturePromise);
  void denial.then((error) => {
    if (error) onDenial?.(error);
  });
  const [draft, signature] = await Promise.all([draftPromise, signaturePromise]);
  if (signal?.aborted) throw new DOMException("A consulta foi invalidada.", "AbortError");
  return { text: draft.text, missing: draft.missing ?? [], signature: signature.signature, signatureKnown: signature.signatureKnown };
}

export function SlashList({
  open,
  replies,
  pending,
  failed,
  activeIndex,
  listId,
  busy,
  notice,
  onActiveIndex,
  onChoose,
}: {
  open: boolean;
  replies: readonly CannedReply[];
  pending: boolean;
  failed: boolean;
  activeIndex: number;
  listId: string;
  busy: boolean;
  notice: string;
  onActiveIndex: (index: number) => void;
  onChoose: (reply: CannedReply) => void;
}) {
  if (!open) return null;
  const boundedIndex = replies.length === 0 ? 0 : Math.min(activeIndex, replies.length - 1);
  return (
    <div>
      <ul id={listId} role="listbox" aria-label="Respostas compartilhadas">
        {pending ? <li>Carregando respostas…</li> : null}
        {failed ? <li>Não foi possível carregar as respostas.</li> : null}
        {!pending && !failed && replies.length === 0 ? <li>Nenhuma resposta para este atalho.</li> : null}
        {replies.map((reply, index) => (
          <li key={reply.id}>
            <button
              type="button"
              role="option"
              id={`${listId}-${reply.id}`}
              aria-selected={index === boundedIndex}
              disabled={busy}
              onMouseEnter={() => onActiveIndex(index)}
              onClick={() => onChoose(reply)}
            >
              /{reply.shortcode} {reply.title}
            </button>
          </li>
        ))}
      </ul>
      {notice ? <p>{notice}</p> : null}
    </div>
  );
}
