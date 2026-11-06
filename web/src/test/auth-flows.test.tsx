import { readFileSync } from "node:fs";
import path from "node:path";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QRCodeSVG } from "qrcode.react";
import { describe, expect, it } from "vitest";
import { currentCsrfToken } from "@/api/client";
import { queryKeys } from "@/lib/query-keys";
import { useSessionStore } from "@/stores/session-store";
import { installPanelOrigin, PanelDouble, startPanelDouble } from "@/test/panel-double";
import { renderAuth } from "@/test/render-auth";

function readFirst(candidates: string[]): string {
  for (const candidate of candidates) {
    try {
      return readFileSync(candidate, "utf8");
    } catch {
      // O teste roda tanto na raiz do repositório quanto em web/.
    }
  }
  throw new Error("arquivo ausente");
}

async function withPanel(double: PanelDouble, path: string, run: () => Promise<void>) {
  const server = await startPanelDouble(double);
  try {
    await installPanelOrigin(server.origin, async () => {
      renderAuth(path);
      await run();
    });
  } finally {
    await server.close();
  }
}

async function enter(email: string, password: string) {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("E-mail"), email);
  await user.type(screen.getByLabelText("Senha"), password);
  await user.click(screen.getByRole("button", { name: "Entrar" }));
  return user;
}

describe("identidade", () => {
  it("entra sem MFA e envia o token de CSRF", async () => {
    const panel = new PanelDouble();
    await withPanel(panel, "/entrar", async () => {
      await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      expect(useSessionStore.getState().principal?.role).toBe("admin");
      expect(currentCsrfToken()).toBe("csrf-publico");
      const user = userEvent.setup();
      await user.click(screen.getByRole("link", { name: "Configurações" }));
      expect(await screen.findByRole("button", { name: "Convidar" })).toBeInTheDocument();
      const listed = panel.requests.find((request) => request.path === "/api/panel/memberships");
      expect(listed?.csrf).toBe("csrf-publico");
      expect(listed?.cookie).toContain("__Host-session=sessao-opaca");
    });
  });

  it("pede o código e aceita o código de recuperação", async () => {
    const panel = new PanelDouble();
    const account = panel.accounts[0];
    if (!account) {
      throw new Error("conta ausente");
    }
    account.mfa = "active";
    await withPanel(panel, "/entrar", async () => {
      const user = await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByLabelText("Código do aplicativo ou de recuperação")).toBeInTheDocument();
      await user.type(screen.getByLabelText("Código do aplicativo ou de recuperação"), "rec-123456");
      await user.click(screen.getByRole("button", { name: "Entrar" }));
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      const attempts = panel.requests.filter((request) => request.path === "/api/panel/session" && request.method === "POST");
      expect(attempts[1]?.body).toMatchObject({ code: "rec-123456" });
      await user.click(screen.getByRole("link", { name: "Configurações" }));
      expect(await screen.findByText("Autenticação em duas etapas: ativa.")).toBeInTheDocument();
      expect(screen.queryByRole("link", { name: "Ativar autenticação em duas etapas" })).not.toBeInTheDocument();
      expect(panel.requests.some((request) => request.path === "/api/panel/mfa/enrollment")).toBe(false);
    });
  });

  it("permite entrar quando um cadastro antigo de MFA está pendente", async () => {
    const panel = new PanelDouble();
    const account = panel.accounts[0];
    if (!account) throw new Error("conta ausente");
    account.mfa = "pending";
    await withPanel(panel, "/entrar", async () => {
      await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      expect(useSessionStore.getState().principal?.mfaEnabled).toBe(false);
    });
  });

  it("mostra os códigos de recuperação e confirma o cadastro", async () => {
    const panel = new PanelDouble();
    const account = panel.accounts[0];
    if (!account) {
      throw new Error("conta ausente");
    }
    await withPanel(panel, "/entrar", async () => {
      const user = await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      await user.click(screen.getByRole("link", { name: "Configurações" }));
      await user.click(await screen.findByRole("link", { name: "Ativar autenticação em duas etapas" }));
      expect(await screen.findByRole("heading", { name: "Autenticação em duas etapas" })).toBeInTheDocument();
      expect(panel.requests.some((request) => request.path === "/api/panel/mfa/enrollment")).toBe(false);
      await user.click(screen.getByRole("button", { name: "Iniciar cadastro" }));
      const qr = await screen.findByRole("img", { name: "QR code para aplicativo autenticador" });
      const reference = render(<QRCodeSVG value="otpauth://totp/Chat%20ISP:ana@provedor.test?secret=SEGREDOBASE32" size={200} marginSize={2} />);
      expect(qr.innerHTML).toBe(reference.container.querySelector("svg")?.innerHTML);
      reference.unmount();
      expect(await screen.findByText("codigo-um")).toBeInTheDocument();
      expect(screen.getByText("codigo-dois")).toBeInTheDocument();
      await user.click(screen.getByText("Não consigo escanear o QR Code"));
      expect(screen.getByText("SEGREDOBASE32")).toBeInTheDocument();
      await user.type(screen.getByLabelText("Código do aplicativo"), "000000");
      await user.click(screen.getByRole("button", { name: "Confirmar código" }));
      expect(await screen.findByRole("alert")).toHaveTextContent("código de autenticação inválido");
      await user.clear(screen.getByLabelText("Código do aplicativo"));
      await user.type(screen.getByLabelText("Código do aplicativo"), "123456");
      await user.click(screen.getByRole("button", { name: "Confirmar código" }));
      expect(await screen.findByText(/Autenticação em duas etapas ativada/)).toBeInTheDocument();
      expect(screen.getByText("codigo-um")).toBeInTheDocument();
      expect(useSessionStore.getState().principal?.mfaEnrollmentRequired).toBe(false);
      expect(useSessionStore.getState().principal?.mfaEnabled).toBe(true);
      await user.click(screen.getByRole("link", { name: "Concluir e voltar às configurações" }));
      expect(await screen.findByText("Autenticação em duas etapas: ativa.")).toBeInTheDocument();
      expect(panel.requests.filter((request) => request.path === "/api/panel/mfa/enrollment")).toHaveLength(1);
    });
  });

  it("limpa a consulta, o estado local e o armazenamento ao sair", async () => {
    const panel = new PanelDouble();
    const server = await startPanelDouble(panel);
    try {
      await installPanelOrigin(server.origin, async () => {
        const { queryClient } = renderAuth("/entrar");
        const user = await enter("ana@provedor.test", "senha-certa");
        expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
        const tenantId = useSessionStore.getState().tenantId ?? "";
        queryClient.setQueryData(queryKeys.conversations(tenantId), [{ id: "c1", label: "sigilosa" }]);
        useSessionStore.getState().setDisplayName("Ana");
        localStorage.setItem("rascunho", "texto local");
        sessionStorage.setItem("filtro", "abertos");
        await user.click(screen.getByRole("button", { name: "Sair" }));
        expect(await screen.findByRole("heading", { name: "Entrar" })).toBeInTheDocument();
        expect(useSessionStore.getState().principal).toBeNull();
        expect(useSessionStore.getState().displayName).toBe("");
        expect(useSessionStore.getState().tenantId).toBeNull();
        expect(currentCsrfToken()).toBeNull();
        expect(queryClient.getQueryData(queryKeys.conversations(tenantId))).toBeUndefined();
        expect(localStorage.getItem("rascunho")).toBeNull();
        expect(sessionStorage.getItem("filtro")).toBeNull();
      });
    } finally {
      await server.close();
    }
  });

  it("esconde a administração de quem não é administrador", async () => {
    const supervisor = new PanelDouble();
    const account = supervisor.accounts[0];
    if (!account) {
      throw new Error("conta ausente");
    }
    account.role = "supervisor";
    await withPanel(supervisor, "/entrar", async () => {
      const user = await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      await user.click(screen.getByRole("link", { name: "Configurações" }));
      expect(await screen.findByRole("heading", { level: 1, name: "Configurações" })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Convidar" })).not.toBeInTheDocument();
      expect(supervisor.requests.some((request) => request.path === "/api/panel/memberships")).toBe(false);
    });
  });

  it("recusa a lista quando o servidor nega, mesmo com papel de administrador", async () => {
    const denied = new PanelDouble();
    await withPanel(denied, "/entrar", async () => {
      const user = await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      expect(useSessionStore.getState().principal?.role).toBe("admin");
      denied.membershipStatus = 403;
      await user.click(screen.getByRole("link", { name: "Configurações" }));
      await waitFor(() => {
        expect(screen.getAllByRole("alert").some((alert) => alert.textContent?.includes("acesso negado"))).toBe(true);
      });
      expect(denied.requests.some((request) => request.method === "GET" && request.path === "/api/panel/memberships")).toBe(true);
      expect(screen.queryByRole("list", { name: "Usuários do provedor" })).not.toBeInTheDocument();
    });
  });

  it("bloqueia a rota de usuários para outro papel", async () => {
    const panel = new PanelDouble();
    const server = await startPanelDouble(panel);
    try {
      await installPanelOrigin(server.origin, async () => {
        useSessionStore.getState().applySession(
          {
            userId: "11111111-1111-4111-8111-111111111111",
            email: "ana@provedor.test",
            tenantId: "22222222-2222-4222-8222-222222222222",
            membershipId: "33333333-3333-4333-8333-333333333333",
            role: "supervisor",
            sessionId: "44444444-4444-4444-8444-444444444444",
            expiresAt: "2026-12-01T12:00:00.000Z",
            mfaEnrollmentRequired: false,
          },
          "csrf-publico",
        );
        renderAuth("/configuracoes/usuarios");
        expect(await screen.findByRole("alert")).toHaveTextContent("não permite esta área");
        expect(panel.requests.some((request) => request.path === "/api/panel/memberships")).toBe(false);
      });
    } finally {
      await server.close();
    }
  });

  it("trata sessão revogada limpando o estado", async () => {
    const panel = new PanelDouble();
    panel.membershipStatus = 401;
    const server = await startPanelDouble(panel);
    try {
      await installPanelOrigin(server.origin, async () => {
        renderAuth("/entrar");
        await enter("ana@provedor.test", "senha-certa");
        expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
        localStorage.setItem("rascunho", "ainda aqui");
        const user = userEvent.setup();
        await user.click(screen.getByRole("link", { name: "Configurações" }));
        expect(await screen.findByText("Sua sessão expirou ou foi revogada. Entre novamente.")).toBeInTheDocument();
        expect(useSessionStore.getState().principal).toBeNull();
        expect(localStorage.getItem("rascunho")).toBeNull();
      });
    } finally {
      await server.close();
    }
  });

  it("aceita convite e confirma a recuperação de senha", async () => {
    const panel = new PanelDouble();
    await withPanel(panel, "/convite?token=convite-ok", async () => {
      const user = userEvent.setup();
      await user.type(screen.getByLabelText("Senha inicial"), "senha-inicial");
      await user.click(screen.getByRole("button", { name: "Criar acesso" }));
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      const accepted = panel.requests.find((request) => request.path === "/api/panel/invitations/accept");
      expect(accepted?.body).toMatchObject({ token: "convite-ok", password: "senha-inicial" });
    });

    const reset = new PanelDouble();
    await withPanel(reset, "/recuperar-senha", async () => {
      const user = userEvent.setup();
      await user.type(screen.getByLabelText("E-mail"), "ana@provedor.test");
      await user.click(screen.getByRole("button", { name: "Enviar pedido" }));
      expect(await screen.findByText("Se o e-mail existir, enviaremos as instruções.")).toBeInTheDocument();
      await user.click(screen.getByRole("link", { name: "Já tenho o token" }));
      await user.type(screen.getByLabelText("Token de recuperação"), "token-ok");
      await user.type(screen.getByLabelText("Nova senha"), "senha-nova-10");
      await user.click(screen.getByRole("button", { name: "Guardar senha" }));
      expect(await screen.findByText(/Senha alterada/)).toBeInTheDocument();
    });
  });

  it("cria, suspende e reativa provedor para o superadmin", async () => {
    const panel = new PanelDouble();
    const account = panel.accounts[0];
    if (!account) {
      throw new Error("conta ausente");
    }
    account.superadmin = true;
    await withPanel(panel, "/entrar", async () => {
      const user = await enter("ana@provedor.test", "senha-certa");
      expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
      await user.click(screen.getByRole("link", { name: "Configurações" }));
      expect(await screen.findByRole("heading", { name: "Provedores da plataforma" })).toBeInTheDocument();
      await user.type(screen.getByLabelText("Limite de armazenamento (bytes)"), "10485760");
      await user.click(screen.getByRole("button", { name: "Criar provedor" }));
      expect(await screen.findByText("America/Sao_Paulo")).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Suspender" }));
      expect(await screen.findByText("Suspenso")).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Reativar" }));
      await waitFor(() => {
        expect(screen.queryByText("Suspenso")).not.toBeInTheDocument();
      });
      await user.type(screen.getByLabelText("E-mail do administrador do provedor"), "admin@novo-provedor.test");
      await user.click(screen.getByRole("button", { name: "Convidar administrador" }));
      expect(await screen.findByText(/Convite enviado por e-mail/)).toBeInTheDocument();
      const invited = panel.requests.find(
        (request) => request.method === "POST" && request.path.endsWith("/admin-invitations"),
      );
      expect(invited?.csrf).toBe("csrf-publico");
      expect(invited?.body).toMatchObject({ email: "admin@novo-provedor.test" });
      const created = panel.requests.find((request) => request.method === "POST" && request.path === "/api/platform/tenants");
      expect(created?.csrf).toBe("csrf-publico");
      expect(created?.body).toMatchObject({ limits: { max_storage_bytes: 10485760 } });
    });
  });
});

describe("cliente gerado", () => {
  it("contém as operações do OpenAPI commitado", () => {
    const spec = JSON.parse(readFirst([
      path.resolve("backend/api/openapi.json"),
      path.resolve("../backend/api/openapi.json"),
    ])) as { paths: Record<string, Record<string, { operationId?: string }>> };
    const schema = readFirst([
      path.resolve("web/src/api/schema.ts"),
      path.resolve("src/api/schema.ts"),
    ]);
    const ids: string[] = [];
    for (const methods of Object.values(spec.paths)) {
      for (const operation of Object.values(methods)) {
        if (operation.operationId) {
          ids.push(operation.operationId);
        }
      }
    }
    expect(ids.length).toBeGreaterThan(10);
    for (const id of ids) {
      expect(schema).toContain(`"${id}"`);
    }
  });
});
