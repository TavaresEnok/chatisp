import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { AppShell } from "@/components/layout/app-shell";
import { IdentityEditor } from "@/features/branding/identity-editor";
import { RetentionPanel } from "@/features/retention/retention-panel";
import { SettingsPage } from "@/pages/settings-page";
import { SessionQuerySync } from "@/providers/session-query-sync";
import { resetSessionStore, useSessionStore, type PanelPrincipal } from "@/stores/session-store";

function principal(tenantId: string, sessionId: string, role = "admin"): PanelPrincipal {
  return {
    tenantId,
    userId: "u",
    sessionId,
    email: "a@b.test",
    membershipId: `m-${tenantId}`,
    role,
    expiresAt: "2099-01-01",
    mfaEnrollmentRequired: false,
  };
}

function EditorKeyed() {
  const current = useSessionStore((state) => state.principal);
  return <IdentityEditor key={`${current?.tenantId ?? ""}:${current?.sessionId ?? ""}`} />;
}

function renderShell(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <SessionQuerySync />
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="painel" element={<EditorKeyed />} />
            <Route path="configuracoes" element={<SettingsPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return client;
}

afterEach(() => {
  resetSessionStore();
  vi.unstubAllGlobals();
});

it("usa Chat ISP quando o provedor ainda não tem nome nem logo", async () => {
  useSessionStore.getState().applySession(principal("t", "s"), "csrf");
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (new URL(url).pathname === "/api/panel/branding") {
      return json({ display_name: "", fallback_name: "Chat ISP", has_logo: false });
    }
    return json({}, 404);
  }));
  renderShell("/painel");
  expect(await screen.findByRole("heading", { name: "Identidade do provedor" })).toBeInTheDocument();
  expect(screen.getAllByText("Chat ISP").length).toBeGreaterThan(0);
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  await waitFor(() => expect(document.title).toBe("Painel — Chat ISP"));
});

it("guarda o nome, envia o logo e remove só o logo", async () => {
  useSessionStore.getState().applySession(principal("t", "s"), "csrf");
  let identity = { display_name: "Antiga", fallback_name: "Chat ISP", has_logo: false, updated_at: "2026-11-04T00:00:00Z" };
  const bodies: unknown[] = [];
  const logoTypes: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path, method, headers, body } = callOf(input, init);
    if (path === "/api/panel/branding/logo" && method === "PUT") {
      logoTypes.push(headers.get("content-type") ?? "");
      identity = { ...identity, has_logo: true, updated_at: "2026-11-04T00:00:02Z" };
      return json(identity);
    }
    if (path === "/api/panel/branding/logo" && method === "DELETE") {
      identity = { ...identity, has_logo: false, updated_at: "2026-11-04T00:00:03Z" };
      return json(identity);
    }
    if (path === "/api/panel/branding/logo") return png();
    if (path === "/api/panel/branding" && method === "PUT") {
      bodies.push(JSON.parse(String(body)));
      identity = { ...identity, display_name: String((bodies.at(-1) as { display_name: string }).display_name), updated_at: "2026-11-04T00:00:01Z" };
      return json(identity);
    }
    if (path === "/api/panel/branding") return json(identity);
    return json({}, 404);
  }));
  const user = userEvent.setup();
  renderShell("/painel");
  const field = await screen.findByLabelText("Nome exibido do provedor");
  expect(field).toHaveValue("Antiga");
  await user.clear(field);
  await user.type(field, "Nexo Fibra");
  await user.upload(screen.getByLabelText("Logo do provedor"), new File([pngBytes()], "logo.png", { type: "image/png" }));
  await user.click(screen.getByRole("button", { name: "Guardar identidade" }));
  expect(await screen.findByText("Identidade guardada. Os prazos de retenção não foram alterados.")).toBeInTheDocument();
  expect(bodies).toEqual([{ display_name: "Nexo Fibra" }]);
  expect(logoTypes).toEqual(["image/png"]);
  expect(screen.getAllByText("Nexo Fibra").length).toBeGreaterThan(0);
  expect(await screen.findByRole("img", { name: "Logo de Nexo Fibra" })).toBeInTheDocument();
  expect(document.title).toBe("Painel — Nexo Fibra");
  await user.click(screen.getByRole("button", { name: "Remover logo" }));
  expect(await screen.findByText("Logo removido. O nome e os prazos de retenção permanecem.")).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByRole("img", { name: "Logo de Nexo Fibra" })).not.toBeInTheDocument());
  expect(screen.getAllByText("Nexo Fibra").length).toBeGreaterThan(0);
});

it("recusa SVG no editor e não chama o servidor", async () => {
  useSessionStore.getState().applySession(principal("t", "s"), "csrf");
  const fetch = vi.fn(async () => json({ display_name: "Nexo", fallback_name: "Chat ISP", has_logo: false }));
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  renderShell("/painel");
  const input = await screen.findByLabelText("Logo do provedor");
  const calls = fetch.mock.calls.length;
  const svg = new File(["<svg></svg>"], "marca.svg", { type: "image/svg+xml" });
  fireEvent.change(input, { target: { files: [svg] } });
  expect(await screen.findByText(/SVG e HTML não são aceitos/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Guardar identidade" }));
  expect(fetch.mock.calls.length).toBe(calls);
});

it("não envia o logo se a sessão muda enquanto o nome ainda responde", async () => {
  useSessionStore.getState().applySession(principal("alfa", "sa"), "csrf-tenant-A");
  let releaseName: (response: Response) => void = () => {};
  const nameGate = new Promise<Response>((resolve) => {
    releaseName = resolve;
  });
  let nameCompleted = false;
  const logoPuts: string[] = [];
  const identities: Record<string, { display_name: string; fallback_name: string; has_logo: boolean }> = {
    alfa: { display_name: "Fibra Alfa", fallback_name: "Chat ISP", has_logo: false },
    beta: { display_name: "Fibra Beta", fallback_name: "Chat ISP", has_logo: false },
  };
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path, method, headers } = callOf(input, init);
    const tenantId = useSessionStore.getState().principal?.tenantId ?? "";
    if (path === "/api/panel/branding/logo" && method === "PUT") {
      logoPuts.push(`${tenantId}:${headers.get("x-csrf-token") ?? ""}`);
      const current = identities[tenantId] ?? {
        display_name: "Fibra Beta",
        fallback_name: "Chat ISP",
        has_logo: false,
      };
      identities[tenantId] = { ...current, has_logo: true };
      return json(identities[tenantId]);
    }
    if (path === "/api/panel/branding" && method === "PUT") {
      const response = await nameGate;
      nameCompleted = true;
      return response;
    }
    if (path === "/api/panel/branding/logo") return png(tenantId === "beta" ? 2 : 9);
    if (path === "/api/panel/branding") return json(identities[tenantId] ?? identities.alfa);
    return json({}, 404);
  });
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  renderShell("/painel");
  expect(await screen.findByLabelText("Nome exibido do provedor")).toHaveValue("Fibra Alfa");
  await user.upload(screen.getByLabelText("Logo do provedor"), new File([pngBytes()], "logo.png", { type: "image/png" }));
  await user.click(screen.getByRole("button", { name: "Guardar identidade" }));
  await waitFor(() => expect(fetch.mock.calls.some((call) => {
    const { path, method, headers } = callOf(call[0], call[1]);
    return path === "/api/panel/branding" && method === "PUT" && headers.get("x-csrf-token") === "csrf-tenant-A";
  })).toBe(true));
  await act(async () => {
    useSessionStore.getState().applySession(principal("beta", "sb"), "csrf-tenant-B");
  });
  await waitFor(() => expect(screen.getByLabelText("Nome exibido do provedor")).toHaveValue("Fibra Beta"));
  releaseName(json({ ...identities.alfa, display_name: "Fibra Alfa Nova" }));
  await waitFor(() => expect(nameCompleted).toBe(true));
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(logoPuts).toEqual([]);
  expect(screen.getByLabelText("Nome exibido do provedor")).toHaveValue("Fibra Beta");
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  expect(screen.queryByText("Fibra Alfa")).not.toBeInTheDocument();
  expect(screen.queryByText("Fibra Alfa Nova")).not.toBeInTheDocument();
  expect(screen.getAllByText("Fibra Beta").length).toBeGreaterThan(0);
});

it("troca de provedor não reaproveita nome nem logo", async () => {
  useSessionStore.getState().applySession(principal("alfa", "sa"), "csrf");
  const names: Record<string, string> = { alfa: "Fibra Alfa", beta: "Fibra Beta" };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { path } = callOf(input, init);
    const tenantId = useSessionStore.getState().principal?.tenantId ?? "";
    if (path === "/api/panel/branding/logo") return png(tenantId === "beta" ? 2 : 9);
    return json({ display_name: names[tenantId] ?? "", fallback_name: "Chat ISP", has_logo: true, content_type: "image/png", updated_at: tenantId });
  }));
  renderShell("/painel");
  expect(await screen.findByRole("img", { name: "Logo de Fibra Alfa" })).toBeInTheDocument();
  useSessionStore.getState().applySession(principal("beta", "sb"), "csrf");
  expect(await screen.findByRole("img", { name: "Logo de Fibra Beta" })).toBeInTheDocument();
  expect(screen.queryByRole("img", { name: "Logo de Fibra Alfa" })).not.toBeInTheDocument();
  expect(screen.queryByText("Fibra Alfa")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Nome exibido do provedor")).toHaveValue("Fibra Beta");
  expect(document.title).toBe("Painel — Fibra Beta");
});

it("guardar o nome na retenção invalida a identidade e mantém a política", async () => {
  const policy = {
    display_name: "Nexo Fibra",
    content_days: 30,
    media_days: null,
    envelope_days: null,
    log_days: null,
    job_days: null,
    automatic_purge: false,
  };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
    if (url.endsWith("/api/panel/retention") && method === "PUT") return json({ ...policy, display_name: "Nexo Atual" });
    if (url.endsWith("/api/panel/retention")) return json(policy);
    if (url.endsWith("/api/panel/plan-limits")) return json({
      agents: 2, max_agents: 2, numbers: 1, max_numbers: 1, storage_bytes: 100, max_storage_bytes: 100,
      note: "O excesso bloqueia cadastro ou upload novo e não apaga o histórico.",
    });
    return json({}, 404);
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["branding", "t", "s"], { identity: { display_name: "Antigo", fallback_name: "Chat ISP", has_logo: true }, logo: null });
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={client}>
      <RetentionPanel />
    </QueryClientProvider>,
  );
  const field = await screen.findByLabelText("Nome do provedor");
  await user.clear(field);
  await user.type(field, "Nexo Atual");
  await user.click(screen.getByRole("button", { name: "Guardar política" }));
  expect(await screen.findByText("Política guardada. O expurgo automático continua desligado.")).toBeInTheDocument();
  await waitFor(() => expect(client.getQueryState(["branding", "t", "s"])?.isInvalidated).toBe(true));
  expect(client.getQueryData(["retention", "policy"])).toMatchObject({ display_name: "Nexo Atual", content_days: 30, automatic_purge: false });
  expect(client.getQueryData(["branding", "t", "s"])).toMatchObject({ identity: { display_name: "Antigo", has_logo: true } });
});

it("mostra o editor só para o administrador nas configurações", async () => {
  vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
  useSessionStore.getState().applySession(principal("t", "s", "agent"), "csrf");
  renderShell("/configuracoes");
  expect(screen.queryByRole("heading", { name: "Identidade do provedor" })).not.toBeInTheDocument();
  cleanup();
  resetSessionStore();
  useSessionStore.getState().applySession(principal("t", "s", "admin"), "csrf");
  renderShell("/configuracoes");
  expect(await screen.findByRole("heading", { name: "Identidade do provedor" })).toBeInTheDocument();
});

function callOf(input: RequestInfo | URL, init?: RequestInit) {
  const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  const path = raw.startsWith("http") ? new URL(raw).pathname : raw.split("?")[0] ?? raw;
  const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
  const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
  return { path, method, headers, body: init?.body };
}

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
}

function pngBytes(mark = 1) {
  return new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, mark]);
}

function png(mark = 1) {
  return new Response(pngBytes(mark), { headers: { "content-type": "image/png" } });
}
