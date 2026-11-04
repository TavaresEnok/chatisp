import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationWorkspace } from "@/features/conversation/conversation-workspace";
import type { ConversationCard } from "@/features/conversation/workspace-types";
import type { PanelPrincipal } from "@/stores/session-store";

const principal: PanelPrincipal = {
  tenantId: "tenant",
  userId: "user",
  sessionId: "session",
  email: "agent@example.com",
  membershipId: "member",
  role: "agent",
  expiresAt: "2099-01-01",
  mfaEnrollmentRequired: false,
};

const card: ConversationCard = {
  id: "conversa-a",
  protocol: "protocolo-a",
  contactLabel: "Cliente da fixture",
  phase: "open",
  assigneeLabel: "",
};

afterEach(() => vi.unstubAllGlobals());

it("mostra o circuito do ERP e mantém o atendimento e o painel externo", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
    title: "circuito do erp aberto",
    status: 503,
    detail: "circuito do erp aberto",
  }), { status: 503, headers: { "content-type": "application/problem+json" } })));
  const onAssume = vi.fn(async () => ({ outcome: "done" as const }));
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ConversationWorkspace
        principal={principal}
        scope={{ tenantId: "tenant", userId: "user:session" }}
        listStatus="ready"
        conversations={[card]}
        detailConversationId={null}
        detailStatus="ready"
        entries={[]}
        onSelect={vi.fn()}
        onAssume={onAssume}
        onSend={vi.fn()}
        onClose={vi.fn()}
        onFarewell={vi.fn()}
        renderSide={() => <section className="painel-externo" aria-label="Painel externo">Painel embutido</section>}
      />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("option"));
  const erp = await screen.findByRole("region", { name: "ERP" });
  expect(await within(erp).findByRole("alert")).toHaveTextContent("circuito do erp aberto");
  expect(screen.getByRole("region", { name: "Painel externo" })).toHaveTextContent("Painel embutido");
  expect(screen.getByRole("form", { name: "Compositor de mensagem" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Assumir" }));
  expect(onAssume).toHaveBeenCalledWith("conversa-a");
});
