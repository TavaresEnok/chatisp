import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { AuditLog } from "@/features/audit/audit-log";
import type { PanelPrincipal } from "@/stores/session-store";

const secret = "mensagem-sigilosa-52998224725";
const admin: PanelPrincipal = {
  tenantId: "tenant-a",
  userId: "user-a",
  sessionId: "session-a",
  membershipId: "member-a",
  role: "admin",
  email: "admin@provedor.test",
  expiresAt: "2099-01-01",
  mfaEnrollmentRequired: false,
};

function page(events: { action: string }[], cursor?: string) {
  return {
    from: "2026-11-01",
    to: "2026-11-03",
    next_cursor: cursor,
    events: events.map((event) => ({
      created_at: "2026-11-03T15:00:00Z",
      action: event.action,
      target_type: "message",
      result: "success",
      body: secret,
      correlation_id: secret,
    })),
  };
}

function renderLog(principal: PanelPrincipal) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AuditLog principal={principal} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

it("mostra só data, ação, tipo e resultado do próprio período", async () => {
  const urls: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    urls.push(url);
    if (url.includes("cursor=pagina-2")) {
      return new Response(JSON.stringify(page([{ action: "audit.um" }])), { headers: { "content-type": "application/json" } });
    }
    return new Response(JSON.stringify(page([{ action: "audit.tres" }], "pagina-2")), { headers: { "content-type": "application/json" } });
  }));
  const user = userEvent.setup();
  renderLog(admin);
  expect(screen.queryByText(secret)).not.toBeInTheDocument();
  expect(screen.queryByText("audit.exemplo")).not.toBeInTheDocument();
  expect(await screen.findByRole("heading", { name: "Auditoria do provedor" })).toBeInTheDocument();
  expect(await screen.findByText("audit.tres")).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Data" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Ação" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Tipo do alvo" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Resultado" })).toBeInTheDocument();
  expect(screen.getByText("message")).toBeInTheDocument();
  expect(screen.getByText("success")).toBeInTheDocument();
  expect(document.body.textContent).not.toContain(secret);
  await user.click(screen.getByRole("button", { name: "Mostrar mais" }));
  expect(await screen.findByText("audit.um")).toBeInTheDocument();
  expect(urls.some((url) => url.includes("cursor=pagina-2"))).toBe(true);
  const start = screen.getByLabelText("Início");
  await user.clear(start);
  await user.type(start, "2026-11-02");
  await waitFor(() => {
    expect(urls.some((url) => url.includes("from=2026-11-02"))).toBe(true);
  });
});

it("não oferece a auditoria a supervisor nem a atendente", () => {
  vi.stubGlobal("fetch", vi.fn());
  const { rerender } = renderLog({ ...admin, role: "supervisor" });
  expect(screen.queryByRole("heading", { name: "Auditoria do provedor" })).not.toBeInTheDocument();
  rerender(
    <QueryClientProvider client={new QueryClient()}>
      <AuditLog principal={{ ...admin, role: "agent" }} />
    </QueryClientProvider>,
  );
  expect(screen.queryByRole("heading", { name: "Auditoria do provedor" })).not.toBeInTheDocument();
  expect(vi.mocked(fetch)).not.toHaveBeenCalled();
});
