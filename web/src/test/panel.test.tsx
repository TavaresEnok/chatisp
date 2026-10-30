import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { areas } from "@/lib/areas";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";

const areaLabels = [
  "Painel",
  "Conversas",
  "Contatos",
  "Equipes",
  "Relatórios",
  "Configurações",
] as const;

const englishUi =
  /\b(loading|error|submit|save|search|settings|dashboard|welcome|retry|cancel|logout|login|users|empty|home|conversations|contacts|teams|reports|not found|no data|undefined)\b/i;

function expectPortugueseUi() {
  const chunks = [document.body.textContent ?? "", document.title];
  document
    .querySelectorAll("[aria-label], [placeholder], [title], [alt]")
    .forEach((element) => {
      for (const attribute of ["aria-label", "placeholder", "title", "alt"]) {
        const value = element.getAttribute(attribute);
        if (value) {
          chunks.push(value);
        }
      }
    });

  expect(chunks.join("\n")).not.toMatch(englishUi);
  expect(document.documentElement.lang).toBe("pt-BR");
}

describe("painel", () => {
  afterEach(() => {
    resetSessionStore();
    vi.unstubAllGlobals();
  });
  it("abre o Painel a partir da raiz", async () => {
    renderPanel("/");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Painel" }),
    ).toBeInTheDocument();
    expect(document.title).toBe("Painel — Chat ISP");
  });

  it("percorre as seis áreas com o teclado", async () => {
    useSessionStore.getState().applySession({
      tenantId: "provedor-a", userId: "operador", sessionId: "sessao-a",
      email: "operador@example.test", membershipId: "membro-a", role: "agent",
      expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
    }, "csrf");
    vi.stubGlobal("WebSocket", undefined);
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (new URL(request.url).pathname.endsWith("/inboxes")) {
        return new Response(JSON.stringify({ inboxes: [] }), {
          status: 200, headers: { "content-type": "application/json" },
        });
      }
      throw new Error(`Requisição inesperada: ${request.url}`);
    }));
    const user = userEvent.setup();
    renderPanel("/painel");

    const nav = screen.getByRole("navigation", { name: "Áreas do painel" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((link) => link.textContent?.trim())).toEqual([
      ...areaLabels,
    ]);

    await user.tab();
    expect(
      screen.getByRole("link", { name: "Ir para o conteúdo" }),
    ).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(document.getElementById("conteudo")).toHaveFocus();

    links[0]?.focus();
    expect(links[0]).toHaveFocus();

    for (const [index, area] of areas.entries()) {
      await user.keyboard("{Enter}");
      expect(
        await screen.findByRole("heading", { level: 1, name: area.label }),
      ).toBeInTheDocument();
      expect(links[index]).toHaveAttribute("aria-current", "page");
      expectPortugueseUi();

      if (index < areas.length - 1) {
        await user.keyboard("{ArrowDown}");
        expect(links[index + 1]).toHaveFocus();
      }
    }

    await user.keyboard("{ArrowDown}");
    expect(links[0]).toHaveFocus();
    await user.keyboard("{End}");
    expect(links[5]).toHaveFocus();
    await user.keyboard("{Home}");
    expect(links[0]).toHaveFocus();
    await user.keyboard("{ArrowUp}");
    expect(links[5]).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(links[4]).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(links[5]).toHaveFocus();
  });

  it("mostra estado vazio honesto nas áreas genéricas", () => {
    for (const area of areas.filter((item) => item.id !== "conversations")) {
      renderPanel(area.path);
      expect(
        screen.getByRole("heading", { level: 1, name: area.label }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { level: 2, name: area.emptyTitle }),
      ).toBeInTheDocument();
      expect(screen.getByText(area.signedOut)).toBeInTheDocument();
      expect(
        screen.queryByRole("list", { name: "Registros da área" }),
      ).not.toBeInTheDocument();
      expect(document.body.textContent ?? "").not.toMatch(/\d/);
      expectPortugueseUi();
      cleanup();
    }
  });

  it("encaminha acesso anônimo às conversas para entrar", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    renderPanel("/conversas");
    expect(await screen.findByRole("heading", { level: 1, name: "Entrar" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 1, name: "Conversas" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Caixa")).not.toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
    expectPortugueseUi();
  });

  it("explica um endereço desconhecido em português", () => {
    renderPanel("/fora-do-painel");
    expect(
      screen.getByRole("heading", { level: 1, name: "Página não encontrada" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Ir para o Painel" })).toBeInTheDocument();
    expectPortugueseUi();
  });

  it("valida o nome local e não envia dado ao servidor", async () => {
    const user = userEvent.setup();
    renderPanel("/configuracoes");

    const field = screen.getByLabelText("Nome de exibição nesta aba");
    await user.click(screen.getByRole("button", { name: "Guardar nesta aba" }));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Informe pelo menos 2 caracteres.",
    );
    expect(field).toHaveAttribute("aria-invalid", "true");

    await user.type(field, "   ");
    await user.click(screen.getByRole("button", { name: "Guardar nesta aba" }));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Informe pelo menos 2 caracteres.",
    );

    await user.clear(field);
    await user.type(field, "Ana");
    await user.click(screen.getByRole("button", { name: "Guardar nesta aba" }));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Nome guardado nesta aba.",
    );
    expect(
      screen.queryByText("Provedor em sessão. Nome nesta aba: Ana."),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(
        "Sessão do servidor ainda não ligada. Nome nesta aba: Ana.",
      ),
    ).toBeInTheDocument();
  });
});
