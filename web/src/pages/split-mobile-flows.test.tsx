import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { ContactsPage } from "@/pages/contacts-page";
import { TeamsPage } from "@/pages/teams-page";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
function session(role: string) {
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role, email: "a@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
}
function view(element: React.ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter>{element}</MemoryRouter></QueryClientProvider>);
}
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });

it("abre o formulário e cria o primeiro setor na visualização móvel", async () => {
  session("admin");
  let created = false;
  const fetcher = vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path === "/api/panel/sectors" && request.method === "POST") {
      expect(await request.json()).toEqual({ name: "Suporte" });
      created = true;
      return json({ id: "s1", name: "Suporte", active: true });
    }
    if (path === "/api/panel/sectors/s1") return json({ membership_ids: [], inbox_ids: [], default_inbox_ids: [] });
    return json({ sectors: created ? [{ id: "s1", name: "Suporte", active: true }] : [] });
  });
  vi.stubGlobal("fetch", fetcher);
  const { container } = view(<TeamsPage />);
  expect(await screen.findByText("Nenhum setor disponível.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Novo setor" }));
  expect(container.querySelector(".teams-page")).toHaveAttribute("data-detail", "true");
  await userEvent.type(screen.getByLabelText("Nome do setor"), "Suporte");
  await userEvent.click(screen.getByRole("button", { name: "Criar setor" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Suporte — Ativo" })).toBeInTheDocument());
  expect(await screen.findByText("Vínculos do setor")).toBeInTheDocument();
});

it("não oferece criação de setor a agentes", async () => {
  session("agent");
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({ sectors: [] })));
  view(<TeamsPage />);
  await screen.findByText("Nenhum setor disponível.");
  expect(screen.queryByRole("button", { name: "Novo setor" })).not.toBeInTheDocument();
});

it("mostra na lista a falha ao abrir contato encontrado por documento", async () => {
  session("admin");
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path === "/api/panel/contacts/document-search") return json({ contact_id: "c1" });
    if (path === "/api/panel/contacts/c1") return json({}, 503);
    return json({ items: [], next_offset: null });
  }));
  const { container } = view(<ContactsPage />);
  await screen.findByText("Nenhum contato encontrado.");
  await userEvent.type(screen.getByLabelText("Buscar CPF/CNPJ exato"), "12345678900");
  await userEvent.click(screen.getByRole("button", { name: "Buscar documento" }));
  const list = screen.getByRole("region", { name: "Diretório de contatos" });
  expect(await within(list).findByRole("alert")).toHaveTextContent("Não foi possível abrir o contato.");
  expect(container.querySelector(".contacts-page")).toHaveAttribute("data-detail", "false");
});
