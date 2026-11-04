import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { BotHookNoticeList, BotHookNoticePanel } from "@/features/bothook/bot-hook-notice";

afterEach(() => {
  vi.unstubAllGlobals();
});

it("mostra o aviso incerto e não o esconde", () => {
  render(
    <BotHookNoticeList
      notices={[
        { id: "p", conversation_id: "c0", state: "pending", notice: "Pendência da automação: o gancho ainda não concluiu esta conversa." },
        { id: "u", conversation_id: "c1", state: "uncertain", notice: "Resultado do gancho incerto. A conversa voltou para a fila humana." },
      ]}
    />,
  );
  const alert = screen.getByRole("alert");
  expect(alert).toHaveTextContent("Resultado do gancho incerto. A conversa voltou para a fila humana.");
  expect(alert).toBeVisible();
  expect(alert.closest("li")).toHaveAttribute("data-state", "uncertain");
  expect(screen.getByRole("status")).toHaveTextContent("Pendência da automação");
});

it("o painel consulta o gancho e mantém o aviso incerto visível", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
    notices: [{ id: "u", conversation_id: "c1", state: "uncertain", notice: "Resultado do gancho incerto. A conversa voltou para a fila humana.", granted_epoch: 4 }],
  }), { status: 200, headers: { "content-type": "application/json" } })));
  render(<BotHookNoticePanel inbox="caixa-1" />);
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("Resultado do gancho incerto");
  expect(alert).toBeVisible();
});
