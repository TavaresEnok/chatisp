import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Schema } from "@/api/attendance";
import { timeline } from "@/features/conversation/attendance-adapter";
import { ConversationWorkspace } from "@/features/conversation/conversation-workspace";
import { attendanceQueryBase } from "@/features/conversation/use-attendance";
import { deliverOutboundMedia } from "@/features/media/send-media";
import type { PanelPrincipal } from "@/stores/session-store";

vi.mock("@/features/media/send-media", async () => {
  const actual = await vi.importActual<typeof import("@/features/media/send-media")>("@/features/media/send-media");
  return { ...actual, deliverOutboundMedia: vi.fn() };
});

vi.mock("@/api/attachments", () => ({
  downloadAttachment: vi.fn(async () => ({ url: "https://arquivos.test/foto", expires_in: 60 })),
}));

const principal: PanelPrincipal = {
  tenantId: "tenant-a",
  userId: "user-a",
  sessionId: "sess-a",
  email: "ana@provedor.test",
  membershipId: "mem-a",
  role: "agent",
  expiresAt: "2099-01-01",
  mfaEnrollmentRequired: false,
};

const versionBox: { version: number; messages: Schema["Message"][] } = { version: 3, messages: [] };

function history(version: number, messages: Schema["Message"][]): Schema["HistoryOutputBody"] {
  return {
    head: {
      id: "conversa-a",
      protocol: "protocolo-a",
      contact_id: "c",
      assignee_id: "a",
      controller: "human",
      controller_epoch: 1,
      created_at: "2026-01-01T00:00:00Z",
      cycle_number: 1,
      last_customer_message_at: "2026-01-01T00:00:00Z",
      status: "open",
      summary: "",
      version,
      waiting_since: "2026-01-01T00:00:00Z",
    },
    messages,
    timeline: [],
    next_cursor: 0,
  };
}

function message(partial: Partial<Schema["Message"]> & Pick<Schema["Message"], "id" | "body">): Schema["Message"] {
  return {
    conversation_id: "conversa-a",
    direction: "outbound",
    external_message_id: partial.id,
    historical: false,
    ingested_at: "2026-01-01T00:00:00Z",
    local_state: "queued",
    origin: "agent",
    trusted_at: "2026-01-01T00:00:00Z",
    visibility: "public",
    ...partial,
  };
}

function Harness() {
  const detail = useQuery({
    queryKey: [...attendanceQueryBase(principal), "inbox-1", "history", "conversa-a"],
    queryFn: async () => history(versionBox.version, versionBox.messages),
  });
  const data = detail.data;
  return (
    <ConversationWorkspace
      principal={principal}
      inboxId="inbox-1"
      connectorType=""
      scope={{ tenantId: principal.tenantId, userId: `${principal.userId}:${principal.sessionId}` }}
      listStatus="ready"
      conversations={[{ id: "conversa-a", protocol: "protocolo-a", contactLabel: "Cliente", phase: "open", assigneeLabel: "Ana" }]}
      detailConversationId="conversa-a"
      detailStatus={data ? "ready" : "loading"}
      entries={data ? timeline(data) : []}
      conversationVersion={data?.head.id === "conversa-a" ? data.head.version : null}
      onSelect={vi.fn()}
      onAssume={vi.fn()}
      onSend={vi.fn()}
      onClose={vi.fn()}
      onFarewell={vi.fn()}
    />
  );
}

function renderHarness() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>,
  );
}

function mockViewport() {
  const prototype = HTMLElement.prototype;
  const height = Object.getOwnPropertyDescriptor(prototype, "clientHeight");
  const width = Object.getOwnPropertyDescriptor(prototype, "clientWidth");
  Object.defineProperty(prototype, "clientHeight", { configurable: true, get: () => 480 });
  Object.defineProperty(prototype, "clientWidth", { configurable: true, get: () => 320 });
  return () => {
    if (height) Object.defineProperty(prototype, "clientHeight", height);
    else Reflect.deleteProperty(prototype, "clientHeight");
    if (width) Object.defineProperty(prototype, "clientWidth", width);
    else Reflect.deleteProperty(prototype, "clientWidth");
  };
}

describe("mídia do workspace sem tempo real", () => {
  let restore = () => undefined as void;
  afterEach(() => {
    restore();
    restore = () => undefined;
    vi.unstubAllGlobals();
  });

  it("atualiza o histórico após o aceite e usa a versão nova depois do conflito", async () => {
    const user = userEvent.setup();
    restore = mockViewport();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array([1, 2, 3]), { headers: { "content-type": "image/jpeg" } })));
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:foto");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    versionBox.version = 3;
    versionBox.messages = [];
    const seen: { version: number; intentKey: string }[] = [];
    vi.mocked(deliverOutboundMedia).mockImplementation(async (input) => {
      seen.push({ version: input.version, intentKey: input.command.intentKey });
      if (seen.length === 1) {
        versionBox.version = 7;
        return { outcome: "failed", detail: "A conversa mudou. Os dados foram atualizados.", conflict: true };
      }
      versionBox.version = 8;
      versionBox.messages = [
        message({
          id: "saida-midia",
          body: "legenda",
          content_kind: "image",
          local_state: "queued",
          content: { type: "image", caption: "legenda", media_status: "stored", mime: "image/jpeg" },
          attachment: { id: "att", name: "fachada.jpg", media_type: "image/jpeg", state: "validated", bytes: 4 },
        }),
      ];
      return { outcome: "accepted" };
    });
    renderHarness();
    await user.click(await screen.findByRole("option", { name: /protocolo-a/ }));
    expect(await screen.findByText("Nenhuma mensagem nesta conversa.")).toBeInTheDocument();
    const file = new File([Uint8Array.of(1, 2, 3)], "fachada.jpg", { type: "image/jpeg" });
    await user.upload(screen.getByLabelText("Anexar arquivo"), file);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByText("A conversa mudou. Os dados foram atualizados.")).toBeInTheDocument();
    expect(seen).toEqual([{ version: 3, intentKey: expect.any(String) }]);

    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByText("Na fila local")).toBeInTheDocument();
    expect(seen).toHaveLength(2);
    expect(seen[1]?.version).toBe(7);
    expect(seen[1]?.intentKey).not.toBe(seen[0]?.intentKey);
    expect(screen.getByText("legenda")).toBeInTheDocument();
  });

  it("repete a versão fixada quando o resultado da mídia fica incerto", async () => {
    const user = userEvent.setup();
    restore = mockViewport();
    versionBox.version = 3;
    versionBox.messages = [];
    const seen: { version: number; intentKey: string }[] = [];
    vi.mocked(deliverOutboundMedia).mockImplementation(async (input) => {
      seen.push({ version: input.version, intentKey: input.command.intentKey });
      if (seen.length === 1) {
        versionBox.version = 9;
        return { outcome: "uncertain", detail: "O resultado do envio é incerto." };
      }
      return { outcome: "accepted" };
    });
    renderHarness();
    await user.click(await screen.findByRole("option", { name: /protocolo-a/ }));
    expect(await screen.findByText("Nenhuma mensagem nesta conversa.")).toBeInTheDocument();
    const file = new File([Uint8Array.of(5)], "nota.webm", { type: "audio/webm" });
    await user.upload(screen.getByLabelText("Anexar arquivo"), file);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByText("O resultado do envio é incerto.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByText(/Mídia aceita pelo conector/)).toBeInTheDocument();
    expect(seen.map((item) => item.version)).toEqual([3, 3]);
    expect(seen[0]?.intentKey).toBe(seen[1]?.intentKey);
  });
});
