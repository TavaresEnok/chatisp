import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationsPage } from "@/pages/conversations-page";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });

it("trata duas negativas consecutivas da mesma conversa e limpa os caches de novo", async () => {
  vi.stubGlobal("WebSocket", undefined);
  let denied = false;
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  const head = { id: "c1", protocol: "P1", contact_id: "contact-1", assignee_id: "m", status: "open", version: 3 };
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path.endsWith("/history")) return denied ? response({ detail: "negado" }, 403) : response({ head, messages: [], timeline: [], next_cursor: 0 });
    if (path.endsWith("/inboxes")) return response({ inboxes: [{ id: "i1", name: "Principal", channel_status: "ready", connector_type: "webchat" }] });
    if (path.endsWith("/queue")) return response({ items: [{ ...head, inbox_id: "i1", contact_label: "Cliente", sector_id: "s1", priority: "normal", unread: false }], counts: { mine: 1, all: 1, unassigned: 0 } });
    if (path.endsWith("/conversations")) return response({ conversations: [head], messages: [], next_cursor: 0 });
    if (path.endsWith("/directory")) return response({ items: [], next_cursor: null });
    if (path.endsWith("/sectors")) return response({ sectors: [] });
    if (path.endsWith("/annotation-tags")) return response([]);
    if (path.endsWith("/annotations")) return response({ priority: "normal", tags: [], notes: [], events: [] });
    if (path.endsWith("/notifications")) return response({ sound: false, browser: false, silent: true });
    return response({});
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const attachment = ["m4a-attachment", "t", "u", "s", "m", "i1", "c1", "sentinel"] as const;
  const policy = ["m4a-policy", "t", "u", "s", "m", "i1", "sentinel"] as const;
  const user = userEvent.setup();
  const view = render(<QueryClientProvider client={qc}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  await user.selectOptions(await screen.findByLabelText("Caixa de atendimento"), "i1");
  await user.click(await screen.findByRole("button", { name: /Protocolo P1/ }));
  expect(await screen.findByLabelText("Mensagem")).toBeInTheDocument();
  for (const round of [1, 2]) {
    qc.setQueryData(attachment, { round });
    qc.setQueryData(policy, { round });
    denied = true;
    await act(async () => { await qc.invalidateQueries({ queryKey: ["attendance", "t", "u", "s", "agent", "m", "i1", "history", "c1"] }); });
    expect(await screen.findByText("Selecione uma conversa para ver o protocolo e o histórico.")).toBeInTheDocument();
    await waitFor(() => expect(qc.getQueryData(attachment)).toBeUndefined());
    expect(qc.getQueryData(policy)).toBeUndefined();
    expect(screen.queryByLabelText("Mensagem")).not.toBeInTheDocument();
    denied = false;
    if (round === 1) {
      await user.click(screen.getByRole("button", { name: /Protocolo P1/ }));
      expect(await screen.findByLabelText("Mensagem")).toBeInTheDocument();
    }
  }
  view.unmount();
  qc.clear();
});
