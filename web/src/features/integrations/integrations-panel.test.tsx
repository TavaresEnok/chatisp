import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { setCsrfToken } from "@/api/client";
import type { ApiToken, WebhookDelivery, WebhookSubscription } from "@/api/integrations";
import { IntegrationsPanel } from "@/features/integrations/integrations-panel";
import type { PanelPrincipal } from "@/stores/session-store";

const admin: PanelPrincipal = {
  tenantId: "tenant",
  userId: "user",
  sessionId: "session",
  email: "admin@example.com",
  membershipId: "member",
  role: "admin",
  expiresAt: "2099-01-01",
  mfaEnrollmentRequired: false,
};

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
  cleanup();
});

function renderPanel(principal: PanelPrincipal = admin) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <IntegrationsPanel principal={principal} />
    </QueryClientProvider>,
  );
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": status >= 400 ? "application/problem+json" : "application/json" },
  });
}

function problem(status: number, detail: string) {
  return json({ title: detail, status, detail }, status);
}

it("mostra o segredo do token uma vez e permite copiar", async () => {
  setCsrfToken("csrf-token");
  const user = userEvent.setup();
  const secret = "segredo-do-token-uma-vez";
  const tokens: ApiToken[] = [];
  const posts: { path: string; body: unknown; csrf: string | null }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "POST" && path === "/api/panel/api-tokens") {
      const body = await request.json();
      posts.push({ path, body, csrf: request.headers.get("X-CSRF-Token") });
      const issued: ApiToken = {
        id: "token-1",
        scopes: body.scopes,
        request_limit: body.request_limit,
        revoked: false,
        created_at: "2026-11-04T12:00:00Z",
      };
      tokens.splice(0, tokens.length, issued);
      return json({ ...issued, secret }, 201);
    }
    if (path === "/api/panel/api-tokens") return json({ tokens });
    if (path === "/api/panel/outbound-webhooks") return json({ subscriptions: [] });
    return problem(404, "não encontrado");
  }));
  renderPanel();
  expect(screen.getByRole("link", { name: "Documentação OpenAPI pública" })).toHaveAttribute("href", "/api/public/openapi.json");
  expect(screen.getByRole("region", { name: "Conector ERP" })).toHaveTextContent("11-conector-erp");
  expect(screen.getByRole("region", { name: "Conector ERP" })).toHaveTextContent("simulado");
  expect(screen.getByRole("region", { name: "Conector ERP" })).toHaveTextContent("desligado");
  await user.click(screen.getByRole("checkbox", { name: "Leitura de contatos" }));
  await user.click(screen.getByRole("button", { name: "Gerar token" }));
  const reveal = await screen.findByLabelText("Segredo do token");
  expect(reveal).toHaveValue(secret);
  expect(posts[0]).toMatchObject({
    path: "/api/panel/api-tokens",
    csrf: "csrf-token",
    body: { scopes: ["contacts.read"], request_limit: 60 },
  });
  await user.click(screen.getByRole("button", { name: "Copiar segredo" }));
  expect(await navigator.clipboard.readText()).toBe(secret);
  expect(await screen.findByText("Segredo copiado.")).toBeInTheDocument();
  const list = await screen.findByRole("list", { name: "Tokens de API" });
  expect(within(list).getByText("token-1")).toBeInTheDocument();
  expect(within(list).queryByDisplayValue(secret)).not.toBeInTheDocument();
  expect(JSON.stringify(tokens)).not.toContain(secret);
  await user.click(screen.getByRole("button", { name: "Ocultar segredo do token" }));
  expect(screen.queryByDisplayValue(secret)).not.toBeInTheDocument();
  expect(screen.queryByText(secret)).not.toBeInTheDocument();
});

it("revoga um token listado", async () => {
  setCsrfToken("csrf-token");
  const tokens: ApiToken[] = [
    {
      id: "token-revogar",
      scopes: ["conversations.read"],
      request_limit: 10,
      revoked: false,
      created_at: "2026-11-04T12:00:00Z",
    },
  ];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "POST" && path === "/api/panel/api-tokens/token-revogar/revoke") {
      expect(request.headers.get("X-CSRF-Token")).toBe("csrf-token");
      const current = tokens[0];
      if (current) current.revoked = true;
      return json({ id: "token-revogar", revoked: true });
    }
    if (path === "/api/panel/api-tokens") return json({ tokens });
    if (path === "/api/panel/outbound-webhooks") return json({ subscriptions: [] });
    return problem(404, "não encontrado");
  }));
  const user = userEvent.setup();
  renderPanel();
  const list = await screen.findByRole("list", { name: "Tokens de API" });
  expect(within(list).getByText("ativo")).toBeInTheDocument();
  await user.click(within(list).getByRole("button", { name: "Revogar" }));
  expect(await within(list).findByText("revogado")).toBeInTheDocument();
  expect(within(list).queryByRole("button", { name: "Revogar" })).not.toBeInTheDocument();
});

it("cadastra, testa e desativa uma assinatura de webhook", async () => {
  setCsrfToken("csrf-hook");
  const secret = "segredo-hmac-uma-vez";
  const subscriptions: WebhookSubscription[] = [];
  const deliveries: WebhookDelivery[] = [
    {
      id: "old",
      type: "message.created",
      source_id: "src-old",
      status: "pending",
      attempt: 1,
      last_status: 0,
      next_attempt_at: "2026-11-04T12:00:01Z",
      occurred_at: "2026-11-04T12:00:00Z",
    },
    {
      id: "new",
      type: "message.created",
      source_id: "src-new",
      status: "delivered",
      attempt: 2,
      last_status: 204,
      next_attempt_at: "2026-11-04T12:01:00Z",
      occurred_at: "2026-11-04T12:00:30Z",
    },
  ];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "POST" && path === "/api/panel/outbound-webhooks") {
      const body = await request.json();
      const issued: WebhookSubscription = { id: "hook-1", url: body.url, events: body.events, enabled: true };
      subscriptions.splice(0, subscriptions.length, issued);
      return json({ ...issued, signing_secret: secret }, 201);
    }
    if (request.method === "POST" && path === "/api/panel/outbound-webhooks/hook-1/disable") {
      const current = subscriptions[0];
      if (current) current.enabled = false;
      return json({ id: "hook-1", enabled: false });
    }
    if (path === "/api/panel/outbound-webhooks/hook-1/deliveries") return json({ deliveries });
    if (path === "/api/panel/outbound-webhooks") return json({ subscriptions });
    if (path === "/api/panel/api-tokens") return json({ tokens: [] });
    return problem(404, "não encontrado");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.type(screen.getByLabelText("URL do webhook"), "https://hooks.example/chat");
  await user.click(screen.getByRole("checkbox", { name: "Mensagem criada" }));
  await user.click(screen.getByRole("checkbox", { name: "Conversa criada" }));
  await user.click(screen.getByRole("button", { name: "Cadastrar assinatura" }));
  expect(await screen.findByLabelText("Segredo do webhook")).toHaveValue(secret);
  const list = await screen.findByRole("list", { name: "Assinaturas de webhook" });
  expect(within(list).getByText("https://hooks.example/chat")).toBeInTheDocument();
  expect(within(list).getByText("Conversa criada, Mensagem criada")).toBeInTheDocument();
  expect(within(list).queryByDisplayValue(secret)).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Ocultar segredo do webhook" }));
  expect(screen.queryByDisplayValue(secret)).not.toBeInTheDocument();
  await user.click(within(list).getByRole("button", { name: "Testar entrega" }));
  expect(await within(list).findByText(/Status da última entrega: entregue \(delivered\), tentativa 2/)).toBeInTheDocument();
  await user.click(within(list).getByRole("button", { name: "Desativar" }));
  expect(await within(list).findByText("desativada")).toBeInTheDocument();
  expect(within(list).queryByRole("button", { name: "Desativar" })).not.toBeInTheDocument();
});

it("mostra erro de URL recusada sem gravar a assinatura", async () => {
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "POST" && path === "/api/panel/outbound-webhooks") {
      return problem(422, "destino ou evento inválido");
    }
    if (path === "/api/panel/outbound-webhooks") return json({ subscriptions: [] });
    if (path === "/api/panel/api-tokens") return json({ tokens: [] });
    return problem(404, "não encontrado");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.type(screen.getByLabelText("URL do webhook"), "http://10.1.2.3/hook");
  await user.click(screen.getByRole("checkbox", { name: "Mensagem criada" }));
  await user.click(screen.getByRole("button", { name: "Cadastrar assinatura" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("URL recusada");
  expect(screen.getByText("Nenhuma assinatura de webhook.")).toBeInTheDocument();
  expect(screen.queryByText("http://10.1.2.3/hook")).not.toBeInTheDocument();
});

it("nega a tela a quem não é administrador do provedor", () => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  for (const principal of [
    { ...admin, role: "supervisor" },
    { ...admin, role: "agent" },
    { ...admin, role: "admin", mfaEnrollmentRequired: true },
  ]) {
    renderPanel(principal);
    expect(screen.getByText("Sem acesso às integrações.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Gerar token" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cadastrar assinatura" })).not.toBeInTheDocument();
    cleanup();
  }
  expect(fetchMock).not.toHaveBeenCalled();
});
