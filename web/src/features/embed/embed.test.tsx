import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ExternalPanel } from "@/features/embed/external-panel";
import { acceptFrameMessage, canEmbed, contextMessage, publish } from "@/features/embed/rules";
import type { PanelPrincipal } from "@/stores/session-store";

const principal: PanelPrincipal = {
  tenantId: "tenant",
  userId: "user",
  sessionId: "session",
  email: "admin@example.com",
  membershipId: "member",
  role: "agent",
  expiresAt: "2099-01-01",
  mfaEnrollmentRequired: false,
};

const context = {
  type: "chatisp.embed.context" as const,
  v: 1 as const,
  conversation_id: "c1",
  protocol: "ATD-1",
  sector_id: "s1",
  inbox_id: "i1",
};

function renderPanel(body: unknown) {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ExternalPanel principal={principal} inboxId="i1" conversationId="c1" />
    </QueryClientProvider>,
  );
}

describe("painel externo", () => {
  it("recusa origem diferente da configurada", () => {
    expect(canEmbed({ embed: true, url: "https://evil.example/painel", origin: "https://erp.example", context })).toBe(false);
    expect(canEmbed({ embed: true, url: "https://erp.example/painel?cpf=1", origin: "https://erp.example", context })).toBe(false);
    expect(canEmbed({ embed: false, url: "https://erp.example/painel", origin: "https://erp.example", context })).toBe(false);
    expect(acceptFrameMessage({ origin: "https://evil.example", source: "janela" }, "https://erp.example", "janela")).toBe(false);
  });

  it("publica contexto sem corpo de mensagem", () => {
    const message = contextMessage({ ...context, body: "CORPO-SECRETO", cpf: "00000000000", secret: "TOKEN", message: "olá" });
    expect(message).toEqual(context);
    expect(JSON.stringify(message)).not.toMatch(/CORPO|00000000000|TOKEN|cpf|secret|olá/);
    const target = { postMessage: vi.fn() };
    publish(target, message, "https://erp.example");
    expect(target.postMessage).toHaveBeenCalledWith(context, "https://erp.example");
  });

  it("não mostra iframe sem url válida", async () => {
    renderPanel({ embed: false });
    await vi.waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(screen.queryByTitle("Painel externo")).not.toBeInTheDocument();
  });

  it("embute somente a url autorizada", async () => {
    renderPanel({ embed: true, url: "https://erp.example/painel", origin: "https://erp.example", context });
    const frame = await screen.findByTitle("Painel externo");
    expect(frame).toHaveAttribute("src", "https://erp.example/painel");
    expect(frame).toHaveAttribute("sandbox", "allow-scripts allow-forms");
    expect(screen.getByRole("link", { name: "Abrir fora do chat" })).toHaveAttribute("href", "https://erp.example/painel");
  });
});
