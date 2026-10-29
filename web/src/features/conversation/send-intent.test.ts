import { describe, expect, it, vi } from "vitest";
import {
  ACCEPTED_NOTICE,
  FAILED_FALLBACK,
  LOST_NOTICE,
  OFFLINE_NOTICE,
  SUBMITTING_NOTICE,
  UNCERTAIN_FALLBACK,
  conversationScopeKey,
  createComposerState,
  reduceComposer,
  selectClientRequestId,
} from "@/features/conversation/send-intent";
import type { ComposerState } from "@/features/conversation/send-intent";

function withDraft(draft: string, patch: Partial<ComposerState> = {}): ComposerState {
  return { ...createComposerState(), draft, ...patch };
}

describe("intenção de envio", () => {
  it("separa a chave por tenant, usuário e conversa", () => {
    const base = { tenantId: "t1", userId: "u1", conversationId: "c1" };
    expect(conversationScopeKey(base)).toBe(conversationScopeKey({ ...base }));
    expect(conversationScopeKey({ ...base, tenantId: "t2" })).not.toBe(
      conversationScopeKey(base),
    );
    expect(conversationScopeKey({ ...base, userId: "u2" })).not.toBe(
      conversationScopeKey(base),
    );
    expect(conversationScopeKey({ ...base, conversationId: "c2" })).not.toBe(
      conversationScopeKey(base),
    );
  });

  it("gera a chave uma vez e reutiliza quando o resultado não chegou", () => {
    const createKey = vi.fn(() => "chave-1");
    const idle = withDraft("oi");
    expect(selectClientRequestId(idle, "reuse-if-pending", createKey)).toBe("chave-1");

    const offline = reduceComposer(idle, {
      type: "submit",
      offline: true,
      intentKey: "chave-1",
      text: "oi",
    });
    expect(offline.draft).toBe("oi");
    expect(offline.notice).toBe(OFFLINE_NOTICE);
    expect(offline.notice).not.toContain(ACCEPTED_NOTICE);
    expect(selectClientRequestId(offline, "reuse-if-pending", createKey)).toBe("chave-1");
    expect(createKey).toHaveBeenCalledTimes(1);

    const lost = reduceComposer(
      reduceComposer(offline, {
        type: "submit",
        offline: false,
        intentKey: "chave-1",
        text: "oi",
      }),
      { type: "result", intentKey: "chave-1", result: { outcome: "lost" } },
    );
    expect(lost.draft).toBe("oi");
    expect(lost.notice).toBe(LOST_NOTICE);
    expect(selectClientRequestId(lost, "reuse-if-pending", createKey)).toBe("chave-1");
    expect(createKey).toHaveBeenCalledTimes(1);
  });

  it("reutiliza a chave quando o resultado fica incerto", () => {
    const createKey = vi.fn(() => "chave-nova");
    const uncertain = reduceComposer(
      reduceComposer(withDraft("oi"), {
        type: "submit",
        offline: false,
        intentKey: "chave-u",
        text: "oi",
      }),
      {
        type: "result",
        intentKey: "chave-u",
        result: { outcome: "uncertain", detail: "" },
      },
    );
    expect(uncertain.draft).toBe("oi");
    expect(uncertain.phase).toBe("uncertain");
    expect(uncertain.pending).toEqual({ intentKey: "chave-u", text: "oi" });
    expect(uncertain.notice).toBe(UNCERTAIN_FALLBACK);
    expect(selectClientRequestId(uncertain, "reuse-if-pending", createKey)).toBe("chave-u");
    expect(createKey).not.toHaveBeenCalled();
    expect(selectClientRequestId(uncertain, "new-decision", createKey)).toBe("chave-nova");
    expect(createKey).toHaveBeenCalledTimes(1);
  });

  it("cria outra chave depois de sucesso, falha conhecida ou nova decisão", () => {
    const createKey = vi
      .fn()
      .mockReturnValueOnce("chave-2")
      .mockReturnValueOnce("chave-3")
      .mockReturnValueOnce("chave-4");
    const pending = withDraft("oi", {
      phase: "uncertain",
      pending: { intentKey: "chave-1", text: "oi" },
      notice: UNCERTAIN_FALLBACK,
    });

    expect(selectClientRequestId(pending, "new-decision", createKey)).toBe("chave-2");
    expect(selectClientRequestId(pending, "reuse-if-pending", createKey)).toBe("chave-1");

    const accepted = reduceComposer(
      reduceComposer(withDraft("oi"), {
        type: "submit",
        offline: false,
        intentKey: "chave-a",
        text: "oi",
      }),
      { type: "result", intentKey: "chave-a", result: { outcome: "accepted" } },
    );
    expect(accepted.draft).toBe("");
    expect(accepted.pending).toBeNull();
    expect(accepted.notice).toBe(ACCEPTED_NOTICE);
    expect(accepted.notice).not.toMatch(/Entregue|Lida/);
    const afterSuccess = reduceComposer(accepted, { type: "edit", draft: "nova" });
    expect(afterSuccess.notice).toBeNull();
    expect(selectClientRequestId(afterSuccess, "reuse-if-pending", createKey)).toBe("chave-3");

    const failed = reduceComposer(
      reduceComposer(withDraft("oi"), {
        type: "submit",
        offline: false,
        intentKey: "chave-f",
        text: "oi",
      }),
      {
        type: "result",
        intentKey: "chave-f",
        result: { outcome: "failed", detail: "O conector recusou a mensagem." },
      },
    );
    expect(failed.draft).toBe("oi");
    expect(failed.pending).toBeNull();
    expect(failed.notice).toBe("O conector recusou a mensagem.");
    expect(selectClientRequestId(failed, "reuse-if-pending", createKey)).toBe("chave-4");
    expect(failed.notice).not.toBe(UNCERTAIN_FALLBACK);
  });

  it("não confirma envio vazio, duplicado ou de outra chave", () => {
    const createKey = vi.fn(() => "chave-x");
    expect(selectClientRequestId(createComposerState(), "reuse-if-pending", createKey)).toBeNull();
    expect(createKey).not.toHaveBeenCalled();

    const submitting = reduceComposer(withDraft("oi"), {
      type: "submit",
      offline: false,
      intentKey: "chave-1",
      text: "oi",
    });
    expect(submitting.notice).toBe(SUBMITTING_NOTICE);
    expect(selectClientRequestId(submitting, "reuse-if-pending", createKey)).toBeNull();
    expect(
      reduceComposer(submitting, {
        type: "result",
        intentKey: "outra",
        result: { outcome: "accepted" },
      }),
    ).toBe(submitting);
    expect(
      reduceComposer(submitting, {
        type: "result",
        intentKey: "chave-1",
        result: { outcome: "failed", detail: " " },
      }).notice,
    ).toBe(FAILED_FALLBACK);
  });
});
