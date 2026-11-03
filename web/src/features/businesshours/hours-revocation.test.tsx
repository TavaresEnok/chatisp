import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HoursSettings } from "@/features/businesshours/hours-settings";
import { hoursKey } from "@/features/businesshours/cache";
import type { PanelPrincipal } from "@/stores/session-store";

const admin: PanelPrincipal = {
  tenantId: "tenant-a", userId: "user-a", sessionId: "sess-a", email: "bia@provedor.test",
  membershipId: "mem-a", role: "admin", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
};

const configuration = {
  version: 2,
  configured: true,
  timezone: "America/Sao_Paulo",
  config: {
    intervals: [{ day: 1, start: 540, end: 720 }],
    welcome: "",
    outside_hours: "Fechado",
    nobody_available: "",
    on_call_sector_id: "",
  },
};

const inboxes = {
  inboxes: [{ id: "inbox-a", name: "Principal", channel_status: "connected", connector_type: "meta_cloud", enabled: true, presented_number: null }],
};

const sectors = {
  sectors: [{ id: "sector-a", name: "Plantão", active: true, legacy_default: false }],
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": status === 200 ? "application/json" : "application/problem+json" },
  });
}

function problem(status: number, detail: string): Response {
  return json({ type: "urn:chatisp:problem:test", title: detail, status, detail }, status);
}

function requestOf(input: RequestInfo | URL): { method: string; path: string } {
  const request = input instanceof Request ? input : new Request(input);
  return { method: request.method.toUpperCase(), path: new URL(request.url).pathname };
}

describe("revogação real das consultas de horário", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each([
    ["inboxes", 403, "Acesso negado."],
    ["inboxes", 404, "Não encontrado."],
    ["sectors", 403, "Acesso negado."],
    ["sectors", 404, "Não encontrado."],
  ] as const)("revalidação HTTP %s %s com PUT pendente não recria a configuração", async (target, status, detail) => {
    let resolvePut: (response: Response) => void = () => undefined;
    let putStarted = false;
    let deny = false;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const { method, path } = requestOf(input);
      if (method === "GET" && path === "/api/panel/inboxes") {
        return deny && target === "inboxes" ? problem(status, detail) : json(inboxes);
      }
      if (method === "GET" && path === "/api/panel/sectors") {
        return deny && target === "sectors" ? problem(status, detail) : json(sectors);
      }
      if (method === "GET" && path === "/api/panel/inboxes/inbox-a/business-hours") return json(configuration);
      if (method === "PUT" && path === "/api/panel/inboxes/inbox-a/business-hours") {
        putStarted = true;
        return new Promise<Response>((resolve) => {
          resolvePut = resolve;
        });
      }
      return problem(500, "Rota inesperada.");
    }));
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><HoursSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    expect(await screen.findByRole("button", { name: "Substituir horário" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Consultar decisão" })).toBeInTheDocument();
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toEqual(configuration);
    await user.click(screen.getByRole("button", { name: "Substituir horário" }));
    await waitFor(() => expect(putStarted).toBe(true));
    deny = true;
    await client.invalidateQueries({ queryKey: hoursKey(admin, target) });
    expect(await screen.findByText(detail)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Substituir horário" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retirar configuração" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Consultar decisão" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Caixa do horário")).not.toBeInTheDocument();
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
    await act(async () => {
      resolvePut(json({ ...configuration, version: 99 }));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
    expect(client.getQueryData(hoursKey(admin, "inboxes"))).toBeUndefined();
    expect(client.getQueryData(hoursKey(admin, "sectors"))).toBeUndefined();
    expect(client.getQueryCache().getAll().some((query) => query.queryKey[0] === "m4a-hours" && query.state.data !== undefined)).toBe(false);
    expect(screen.queryByText("Horário gravado.")).not.toBeInTheDocument();
    expect(screen.queryByText(/Versão 99/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Substituir horário" })).not.toBeInTheDocument();
  });
});
