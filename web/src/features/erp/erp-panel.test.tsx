import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { setCsrfToken } from "@/api/client";
import { ErpPanel } from "@/features/erp/erp-panel";
import type { ErpLink } from "@/api/erp";
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

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
});

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": status === 200 ? "application/json" : "application/problem+json" },
  });
}

function problem(status: number, detail: string) {
  return json({ title: detail, status, detail }, status);
}

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ErpPanel principal={principal} conversationId="c1" localProtocol="ATD-9" />
    </QueryClientProvider>,
  );
}

it("vincula o contato, identifica por referência e mostra o resumo", async () => {
  setCsrfToken("csrf-erp");
  const links: ErpLink[] = [];
  const posts: { path: string; body: unknown; csrf: string | null }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "POST") {
      const body = await request.json();
      posts.push({ path, body, csrf: request.headers.get("X-CSRF-Token") });
      if (path.endsWith("/identify")) {
        return json({
          simulated: true,
          outcome: "ok",
          customer: { external_id: "fake-customer-casa", contract_id: "fake-contract-casa", display_name: "Cadastro identificado" },
        });
      }
      if (path.endsWith("/protocol")) {
        const saved = links[0];
        if (saved) saved.external_protocol = body.external_protocol;
        return json({ simulated: true, local_protocol: "ATD-9", links: saved ? [saved] : [] });
      }
      const link: ErpLink = {
        id: "link-1",
        contact_id: "contact-1",
        conversation_id: "c1",
        external_customer_id: body.external_customer_id,
        external_contract_id: body.external_contract_id,
        selected: body.selected,
      };
      links.splice(0, links.length, link);
      return json({ simulated: true, links: [link] });
    }
    if (path.endsWith("/summary")) {
      return json({
        simulated: true,
        outcome: "ok",
        customer: { external_id: "fake-customer-casa", contract_id: "fake-contract-casa", display_name: "Cadastro simulado — casa" },
        consulted_at: "2026-11-04T12:00:00Z",
      });
    }
    if (path.endsWith("/links")) return json({ simulated: true, links: [...links] });
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.type(await screen.findByLabelText("Cliente no ERP"), "+5511999999999");
  await user.click(screen.getByRole("button", { name: "Vincular" }));
  expect(await screen.findByText("Telefone não escolhe o vínculo.")).toBeInTheDocument();
  expect(posts).toHaveLength(0);
  await user.clear(screen.getByLabelText("Cliente no ERP"));
  await user.type(screen.getByLabelText("Cliente no ERP"), "fake-customer-casa");
  await user.type(screen.getByLabelText("Contrato no ERP"), "fake-contract-casa");
  await user.click(screen.getByRole("button", { name: "Vincular" }));
  expect(await screen.findByText("Cadastro simulado — casa · fake-contract-casa")).toBeInTheDocument();
  expect(posts[0]).toMatchObject({
    path: "/api/panel/conversations/c1/erp/links",
    csrf: "csrf-erp",
    body: { external_customer_id: "fake-customer-casa", external_contract_id: "fake-contract-casa", selected: true },
  });
  expect(screen.getByText("Selecionado")).toBeInTheDocument();
  expect(screen.getByText("Simulação. O cadastro comercial não fica neste chat.")).toBeInTheDocument();
  await user.type(screen.getByLabelText("Referência autorizada"), " ref-casa ");
  await user.type(screen.getByLabelText("Chave local da identificação"), "id-casa");
  await user.click(screen.getByRole("button", { name: "Identificar" }));
  expect(await screen.findByText("Identificação: Cadastro identificado · fake-contract-casa")).toBeInTheDocument();
  expect(posts[1]).toMatchObject({
    path: "/api/panel/conversations/c1/erp/identify",
    body: { reference: "ref-casa", local_key: "id-casa" },
  });
  expect(screen.getByText("Protocolo local: ATD-9")).toBeInTheDocument();
  await user.type(screen.getByLabelText("Protocolo no ERP"), "ERP-99");
  await user.click(screen.getByRole("button", { name: "Associar protocolo" }));
  await waitFor(() => expect(posts.some((item) => item.path.endsWith("/protocol"))).toBe(true));
  expect(posts.find((item) => item.path.endsWith("/protocol"))?.body).toEqual({ external_protocol: "ERP-99" });
  expect(screen.getByText("Protocolo local: ATD-9")).toBeInTheDocument();
  expect(await screen.findByText("Protocolo no ERP: ERP-99")).toBeInTheDocument();
});

it("lista faturas e só envia a segunda via depois da confirmação", async () => {
  const copies: unknown[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    if (request.method === "POST" && url.pathname.endsWith("/second-copy")) {
      copies.push(await request.json());
      return json({ simulated: true, outcome: "ok", pix_code: "simulacao:fake-pix-casa-1", message_id: "m1", attempt_id: "a1" });
    }
    if (url.pathname.endsWith("/invoices")) {
      expect(url.searchParams.get("verification")).toBe("verificacao-1");
      expect(url.searchParams.get("local_key")).toBe("fat-1");
      return json({
        simulated: true,
        outcome: "ok",
        invoices: [{ external_id: "fake-inv-casa-1", label: "fatura simulada em aberto" }],
      });
    }
    if (url.pathname.endsWith("/summary")) {
      return json({ simulated: true, outcome: "ok", customer: { external_id: "fake-customer-casa", display_name: "Casa" } });
    }
    if (url.pathname.endsWith("/links")) {
      return json({
        links: [{ id: "link-1", contact_id: "contact-1", conversation_id: "c1", external_customer_id: "fake-customer-casa", external_contract_id: "fake-contract-casa", selected: true }],
      });
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  expect(await screen.findByText("Casa")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Consultar faturas" })).toBeDisabled();
  await user.type(screen.getByLabelText("Verificação"), "verificacao-1");
  await user.type(screen.getByLabelText("Chave local das faturas"), "fat-1");
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  const panel = await screen.findByRole("region", { name: "ERP" });
  expect(await within(panel).findByText("fatura simulada em aberto")).toBeInTheDocument();
  expect(panel).not.toHaveTextContent(/R\$/);
  await user.click(screen.getByRole("button", { name: "Enviar Pix fatura simulada em aberto" }));
  expect(copies).toHaveLength(0);
  const dialog = screen.getByRole("dialog", { name: "Confirmar segunda via" });
  await user.click(within(dialog).getByRole("button", { name: "Cancelar" }));
  expect(copies).toHaveLength(0);
  expect(screen.queryByRole("dialog", { name: "Confirmar segunda via" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Enviar Pix fatura simulada em aberto" }));
  await user.click(screen.getByRole("button", { name: "Confirmar envio" }));
  expect(await screen.findByText(/Segunda via enfileirada/)).toHaveTextContent("simulacao:fake-pix-casa-1");
  expect(copies).toHaveLength(1);
  expect(copies[0]).toMatchObject({
    invoice_id: "fake-inv-casa-1",
    verification: "verificacao-1",
    local_key: expect.any(String),
    client_request_id: expect.any(String),
    artifact: "pix",
  });
});

it("mostra circuito aberto e capacidade indisponível sem apagar o vínculo", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => problem(503, "circuito do erp aberto")));
  const user = userEvent.setup();
  const view = renderPanel();
  const panel = await screen.findByRole("region", { name: "ERP" });
  expect(await within(panel).findByRole("alert")).toHaveTextContent("circuito do erp aberto");
  await user.type(within(panel).getByLabelText("Cliente no ERP"), "fake-customer-casa");
  expect(within(panel).getByLabelText("Cliente no ERP")).toHaveValue("fake-customer-casa");
  view.unmount();

  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path.endsWith("/summary")) return json({ outcome: "unavailable", reason: "capacidade indisponível", simulated: true });
    if (path.endsWith("/links")) {
      return json({
        links: [{ id: "link-1", contact_id: "contact-1", conversation_id: "c1", external_customer_id: "fake-customer-casa", selected: true, customer_url: "https://erp.provedor.example/clientes/fake-customer-casa" }],
      });
    }
    return problem(500, "rota inesperada");
  }));
  renderPanel();
  expect(await screen.findByRole("alert")).toHaveTextContent("capacidade indisponível");
  expect(screen.getByText("fake-customer-casa")).toBeInTheDocument();
  const open = screen.getByRole("link", { name: "Abrir cadastro no ERP (fake-customer-casa)" });
  expect(open).toHaveAttribute("href", "https://erp.provedor.example/clientes/fake-customer-casa");
  expect(open).toHaveAttribute("rel", "noopener noreferrer");
  expect(open).toHaveAttribute("target", "_blank");
  expect(screen.getByRole("button", { name: "Vincular" })).toBeEnabled();
});

it("oculta as ações sem permissão da conversa e sem permissão de fatura", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => problem(403, "acesso negado")));
  const view = renderPanel();
  expect(await screen.findByText("Sem permissão para o ERP desta conversa.")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Vincular" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Identificar" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Consultar faturas" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar Pix/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Associar protocolo" })).not.toBeInTheDocument();
  view.unmount();

  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path.endsWith("/invoices")) return problem(403, "acesso negado");
    if (path.endsWith("/summary")) return json({ outcome: "ok", customer: { external_id: "fake-customer-casa", display_name: "Casa" } });
    if (path.endsWith("/links")) {
      return json({
        links: [{ id: "link-1", contact_id: "contact-1", conversation_id: "c1", external_customer_id: "fake-customer-casa", selected: true }],
      });
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.type(await screen.findByLabelText("Verificação"), "verificacao-1");
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  expect(await screen.findByText("Sem permissão para faturas e segunda via.")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Consultar faturas" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar Pix/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar documento/ })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Vincular" })).toBeEnabled();
});

it("troca a seleção sem duplicar vínculos e esconde as faturas do vínculo anterior", async () => {
  setCsrfToken("csrf-erp");
  let nextId = 3;
  const rows: ErpLink[] = [
    {
      id: "link-casa",
      contact_id: "contact-1",
      conversation_id: "c1",
      external_customer_id: "fake-customer-casa",
      external_contract_id: "fake-contract-casa",
      external_protocol: "ERP-99",
      selected: true,
    },
    {
      id: "link-comercio",
      contact_id: "contact-1",
      conversation_id: "c1",
      external_customer_id: "fake-customer-comercio",
      external_contract_id: "fake-contract-comercio",
      selected: false,
    },
  ];
  const calls: { method: string; path: string }[] = [];
  let invoiceReads = 0;
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    calls.push({ method: request.method, path: url.pathname });
    if (request.method === "GET" && url.pathname.endsWith("/links")) {
      return json({ simulated: true, links: rows.map((item) => ({ ...item })) });
    }
    if (request.method === "GET" && url.pathname.endsWith("/summary")) {
      const chosen = rows.find((item) => item.selected);
      const casa = chosen?.external_customer_id === "fake-customer-casa";
      return json({
        simulated: true,
        outcome: "ok",
        customer: {
          external_id: chosen?.external_customer_id,
          contract_id: chosen?.external_contract_id,
          display_name: casa ? "Casa" : "Comércio",
        },
      });
    }
    if (request.method === "GET" && url.pathname.endsWith("/invoices")) {
      invoiceReads += 1;
      const chosen = rows.find((item) => item.selected);
      const casa = chosen?.external_customer_id === "fake-customer-casa";
      return json({
        simulated: true,
        outcome: "ok",
        invoices: [{ external_id: casa ? "fake-inv-casa-1" : "fake-inv-comercio-1", label: casa ? "fatura da casa" : "fatura do comércio" }],
      });
    }
    if (request.method === "POST" && url.pathname.endsWith("/links")) {
      const body = await request.json() as { external_customer_id: string; external_contract_id?: string; selected: boolean };
      if (body.selected) {
        for (const item of rows) item.selected = false;
      }
      const created: ErpLink = {
        id: `link-${nextId}`,
        contact_id: "contact-1",
        conversation_id: "c1",
        external_customer_id: body.external_customer_id,
        external_contract_id: body.external_contract_id,
        selected: body.selected,
      };
      rows.push(created);
      nextId += 1;
      return json({ simulated: true, links: [created] });
    }
    if (request.method === "DELETE" && url.pathname.includes("/links/")) {
      const id = decodeURIComponent(url.pathname.split("/").at(-1) ?? "");
      const index = rows.findIndex((item) => item.id === id);
      if (index < 0) return problem(404, "não encontrado");
      rows.splice(index, 1);
      return json({});
    }
    if (request.method === "POST" && url.pathname.endsWith("/protocol")) {
      const body = await request.json() as { external_protocol: string };
      const chosen = rows.find((item) => item.selected);
      if (chosen) chosen.external_protocol = body.external_protocol;
      return json({ simulated: true, local_protocol: "ATD-9", links: chosen ? [{ ...chosen }] : [] });
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  const list = await screen.findByRole("list", { name: "Vínculos" });
  expect(within(list).getByText("Selecionado")).toBeInTheDocument();
  expect(screen.getByText("Protocolo no ERP: ERP-99")).toBeInTheDocument();
  await user.type(screen.getByLabelText("Verificação"), "verificacao-1");
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  expect(await screen.findByText("fatura da casa")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Enviar Pix fatura da casa" })).toBeEnabled();
  expect(invoiceReads).toBe(1);

  await user.click(screen.getByRole("button", { name: "Selecionar vínculo fake-customer-comercio" }));
  await waitFor(() => expect(screen.getByText("Comércio · fake-contract-comercio")).toBeInTheDocument());
  const afterTrade = within(screen.getByRole("list", { name: "Vínculos" })).getAllByRole("listitem");
  expect(afterTrade).toHaveLength(2);
  expect(afterTrade.filter((item) => item.textContent?.includes("fake-customer-casa"))).toHaveLength(1);
  expect(afterTrade.filter((item) => item.textContent?.includes("fake-customer-comercio"))).toHaveLength(1);
  expect(afterTrade.filter((item) => item.textContent?.includes("Selecionado"))).toHaveLength(1);
  expect(screen.queryByText("fatura da casa")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar Pix/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar documento/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("dialog", { name: "Confirmar segunda via" })).not.toBeInTheDocument();
  expect(screen.queryByText("Protocolo no ERP: ERP-99")).not.toBeInTheDocument();
  expect(invoiceReads).toBe(1);
  expect(calls.filter((call) => call.method === "POST" && call.path.endsWith("/links"))).toHaveLength(1);
  expect(calls.some((call) => call.method === "DELETE" && call.path.endsWith("/links/link-comercio"))).toBe(true);

  await user.click(screen.getByRole("button", { name: "Selecionar vínculo fake-customer-casa" }));
  await waitFor(() => {
    expect(screen.getByText("Casa · fake-contract-casa")).toBeInTheDocument();
    expect(screen.getByText("Protocolo no ERP: ERP-99")).toBeInTheDocument();
  });
  const restored = within(screen.getByRole("list", { name: "Vínculos" })).getAllByRole("listitem");
  expect(restored).toHaveLength(2);
  expect(restored.filter((item) => item.textContent?.includes("fake-customer-casa"))).toHaveLength(1);
  expect(restored.filter((item) => item.textContent?.includes("fake-customer-comercio"))).toHaveLength(1);
  expect(calls.filter((call) => call.method === "POST" && call.path.endsWith("/links"))).toHaveLength(2);
  expect(calls.filter((call) => call.method === "DELETE" && call.path.includes("/links/"))).toHaveLength(2);
  expect(calls.some((call) => call.method === "POST" && call.path.endsWith("/protocol"))).toBe(true);
  expect(screen.queryByText("fatura da casa")).not.toBeInTheDocument();
  expect(invoiceReads).toBe(1);

  await user.click(screen.getByRole("button", { name: "Remover vínculo fake-customer-casa" }));
  await waitFor(() => expect(within(screen.getByRole("list", { name: "Vínculos" })).queryByText(/fake-customer-casa/)).not.toBeInTheDocument());
  expect(screen.queryByText("fatura da casa")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar Pix/ })).not.toBeInTheDocument();
  expect(screen.getByText("Escolha um vínculo para ver o resumo.")).toBeInTheDocument();
});

it("mostra a cópia antiga quando a troca de seleção não consegue removê-la", async () => {
  setCsrfToken("csrf-erp");
  const rows: ErpLink[] = [
    {
      id: "link-casa",
      contact_id: "contact-1",
      conversation_id: "c1",
      external_customer_id: "fake-customer-casa",
      external_contract_id: "fake-contract-casa",
      external_protocol: "ERP-99",
      selected: true,
    },
    {
      id: "link-comercio",
      contact_id: "contact-1",
      conversation_id: "c1",
      external_customer_id: "fake-customer-comercio",
      external_contract_id: "fake-contract-comercio",
      selected: false,
    },
  ];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname.endsWith("/links")) return json({ simulated: true, links: rows.map((item) => ({ ...item })) });
    if (request.method === "GET" && url.pathname.endsWith("/summary")) {
      return json({ simulated: true, outcome: "ok", customer: { external_id: "fake-customer-casa", display_name: "Casa" } });
    }
    if (request.method === "POST" && url.pathname.endsWith("/links")) {
      const body = await request.json() as { external_customer_id: string; external_contract_id?: string; selected: boolean };
      if (body.selected) {
        for (const item of rows) item.selected = false;
      }
      rows.push({
        id: "link-comercio-2",
        contact_id: "contact-1",
        conversation_id: "c1",
        external_customer_id: body.external_customer_id,
        external_contract_id: body.external_contract_id,
        selected: true,
      });
      return json({ simulated: true });
    }
    if (request.method === "DELETE") return problem(500, "falha ao remover");
    if (request.method === "POST" && url.pathname.endsWith("/protocol")) return problem(500, "protocolo não deveria ser chamado");
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.click(await screen.findByRole("button", { name: "Selecionar vínculo fake-customer-comercio" }));
  await waitFor(() => {
    expect(screen.getByText(/cópia antiga/)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Vínculos" }).querySelectorAll("li")).toHaveLength(3);
  });
  const duplicated = within(screen.getByRole("list", { name: "Vínculos" })).getAllByRole("listitem");
  expect(duplicated.filter((item) => item.textContent?.includes("fake-customer-comercio"))).toHaveLength(2);
  expect(screen.queryByText("Protocolo no ERP: ERP-99")).not.toBeInTheDocument();
});

it("consulta faturas da casa e do comércio com chaves distintas na mesma verificação", async () => {
  setCsrfToken("csrf-erp");
  let nextId = 3;
  const rows: ErpLink[] = [
    {
      id: "link-casa",
      contact_id: "contact-1",
      conversation_id: "c1",
      external_customer_id: "fake-customer-casa",
      external_contract_id: "fake-contract-casa",
      selected: true,
    },
    {
      id: "link-comercio",
      contact_id: "contact-1",
      conversation_id: "c1",
      external_customer_id: "fake-customer-comercio",
      external_contract_id: "fake-contract-comercio",
      selected: false,
    },
  ];
  const invoiceKeys: string[] = [];
  const seen = new Map<string, string>();
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname.endsWith("/links")) {
      return json({ simulated: true, links: rows.map((item) => ({ ...item })) });
    }
    if (request.method === "GET" && url.pathname.endsWith("/summary")) {
      const chosen = rows.find((item) => item.selected);
      const casa = chosen?.external_customer_id === "fake-customer-casa";
      return json({
        simulated: true,
        outcome: "ok",
        customer: {
          external_id: chosen?.external_customer_id,
          contract_id: chosen?.external_contract_id,
          display_name: casa ? "Casa" : "Comércio",
        },
      });
    }
    if (request.method === "GET" && url.pathname.endsWith("/invoices")) {
      const chosen = rows.find((item) => item.selected);
      const customer = chosen?.external_customer_id ?? "";
      const localKey = url.searchParams.get("local_key") ?? "";
      const prior = seen.get(localKey);
      if (prior && prior !== customer) return problem(409, "conflito de chave");
      if (!prior) seen.set(localKey, customer);
      invoiceKeys.push(localKey);
      const casa = customer === "fake-customer-casa";
      return json({
        simulated: true,
        outcome: "ok",
        invoices: [{ external_id: casa ? "fake-inv-casa-1" : "fake-inv-comercio-1", label: casa ? "fatura da casa" : "fatura do comércio" }],
      });
    }
    if (request.method === "POST" && url.pathname.endsWith("/links")) {
      const body = await request.json() as { external_customer_id: string; external_contract_id?: string; selected: boolean };
      if (body.selected) {
        for (const item of rows) item.selected = false;
      }
      rows.push({
        id: `link-${nextId}`,
        contact_id: "contact-1",
        conversation_id: "c1",
        external_customer_id: body.external_customer_id,
        external_contract_id: body.external_contract_id,
        selected: body.selected,
      });
      nextId += 1;
      return json({ simulated: true });
    }
    if (request.method === "DELETE" && url.pathname.includes("/links/")) {
      const id = decodeURIComponent(url.pathname.split("/").at(-1) ?? "");
      const index = rows.findIndex((item) => item.id === id);
      if (index < 0) return problem(404, "não encontrado");
      rows.splice(index, 1);
      return json({});
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  expect(await screen.findByText("Casa · fake-contract-casa")).toBeInTheDocument();
  await user.type(screen.getByLabelText("Verificação"), "verificacao-1");
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  expect(await screen.findByText("fatura da casa")).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Selecionar vínculo fake-customer-comercio" }));
  await waitFor(() => expect(screen.getByText("Comércio · fake-contract-comercio")).toBeInTheDocument());
  expect(screen.queryByText("fatura da casa")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  expect(await screen.findByText("fatura do comércio")).toBeInTheDocument();
  expect(screen.queryByText("conflito de chave")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Selecionar vínculo fake-customer-casa" }));
  await waitFor(() => expect(screen.getByText("Casa · fake-contract-casa")).toBeInTheDocument());
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  expect(await screen.findByText("fatura da casa")).toBeInTheDocument();
  expect(invoiceKeys).toHaveLength(3);
  expect(invoiceKeys[0]).not.toBe("");
  expect(invoiceKeys[1]).not.toBe(invoiceKeys[0]);
  expect(invoiceKeys[2]).toBe(invoiceKeys[0]);
  expect(screen.queryByText("conflito de chave")).not.toBeInTheDocument();
});

it("não confirma a segunda via quando o resultado é incerto, nem no replay", async () => {
  const copies: Record<string, string>[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    if (request.method === "POST" && url.pathname.endsWith("/second-copy")) {
      copies.push(await request.json() as Record<string, string>);
      return json({ simulated: true, outcome: "uncertain", reason: "uncertain", replayed: copies.length > 1 });
    }
    if (url.pathname.endsWith("/invoices")) {
      return json({
        simulated: true,
        outcome: "ok",
        invoices: [{ external_id: "fake-inv-casa-1", label: "fatura simulada em aberto" }],
      });
    }
    if (url.pathname.endsWith("/summary")) {
      return json({ simulated: true, outcome: "ok", customer: { external_id: "fake-customer-casa", display_name: "Casa" } });
    }
    if (url.pathname.endsWith("/links")) {
      return json({
        links: [{ id: "link-1", contact_id: "contact-1", conversation_id: "c1", external_customer_id: "fake-customer-casa", selected: true }],
      });
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.type(await screen.findByLabelText("Verificação"), "verificacao-1");
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  await user.click(await screen.findByRole("button", { name: "Enviar Pix fatura simulada em aberto" }));
  await user.click(screen.getByRole("button", { name: "Confirmar envio" }));
  expect(await screen.findByText("O ERP não confirmou o envio. Nenhuma mensagem foi enfileirada.")).toBeInTheDocument();
  expect(screen.queryByText(/Segunda via enfileirada/)).not.toBeInTheDocument();
  expect(screen.queryByText(/Envio já registrado/)).not.toBeInTheDocument();
  expect(screen.getByRole("dialog", { name: "Confirmar segunda via" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Confirmar envio" }));
  expect(await screen.findByText("Consulta repetida: o ERP segue sem confirmar o envio. Nenhuma mensagem foi enfileirada.")).toBeInTheDocument();
  expect(copies).toHaveLength(2);
  expect(copies[1]?.local_key).toBe(copies[0]?.local_key);
  expect(copies[1]?.client_request_id).toBe(copies[0]?.client_request_id);
  expect(screen.queryByText(/Segunda via enfileirada/)).not.toBeInTheDocument();
  expect(screen.queryByText(/Envio já registrado/)).not.toBeInTheDocument();
});

it("não trata HTTP 200 sem o par da outbox como envio", async () => {
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    if (request.method === "POST" && url.pathname.endsWith("/second-copy")) {
      return json({ simulated: true, outcome: "ok", pix_code: "simulacao:nao-enfileirado" });
    }
    if (url.pathname.endsWith("/invoices")) {
      return json({
        outcome: "ok",
        invoices: [{ external_id: "fake-inv-casa-1", label: "fatura simulada em aberto" }],
      });
    }
    if (url.pathname.endsWith("/summary")) {
      return json({ outcome: "ok", customer: { external_id: "fake-customer-casa", display_name: "Casa" } });
    }
    if (url.pathname.endsWith("/links")) {
      return json({
        links: [{ id: "link-1", contact_id: "contact-1", conversation_id: "c1", external_customer_id: "fake-customer-casa", selected: true }],
      });
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  await user.type(await screen.findByLabelText("Verificação"), "verificacao-1");
  await user.click(screen.getByRole("button", { name: "Consultar faturas" }));
  await user.click(await screen.findByRole("button", { name: "Enviar Pix fatura simulada em aberto" }));
  await user.click(screen.getByRole("button", { name: "Confirmar envio" }));
  expect(await screen.findByText("A segunda via não foi enfileirada.")).toBeInTheDocument();
  expect(screen.queryByText(/Segunda via enfileirada/)).not.toBeInTheDocument();
  expect(screen.queryByText("simulacao:nao-enfileirado")).not.toBeInTheDocument();
  expect(screen.getByRole("dialog", { name: "Confirmar segunda via" })).toBeInTheDocument();
});

it("esconde as ações quando uma ação da conversa é recusada depois da lista de vínculos", async () => {
  setCsrfToken("csrf-erp");
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    if (request.method === "POST" && url.pathname.endsWith("/identify")) return problem(403, "setor da conversa negado");
    if (url.pathname.endsWith("/summary")) {
      return json({ outcome: "ok", customer: { external_id: "fake-customer-casa", display_name: "Casa" } });
    }
    if (url.pathname.endsWith("/links")) {
      return json({
        links: [{ id: "link-1", contact_id: "contact-1", conversation_id: "c1", external_customer_id: "fake-customer-casa", selected: true }],
      });
    }
    return problem(500, "rota inesperada");
  }));
  const user = userEvent.setup();
  renderPanel();
  expect(await screen.findByText("Casa")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Vincular" })).toBeEnabled();
  await user.type(screen.getByLabelText("Referência autorizada"), "ref-casa");
  await user.click(screen.getByRole("button", { name: "Identificar" }));
  expect(await screen.findByText("Sem permissão para o ERP desta conversa.")).toBeInTheDocument();
  expect(screen.getByText("setor da conversa negado")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Vincular" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Identificar" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Consultar faturas" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Associar protocolo" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Enviar Pix/ })).not.toBeInTheDocument();
  expect(screen.queryByText("Sem permissão para faturas e segunda via.")).not.toBeInTheDocument();
});
