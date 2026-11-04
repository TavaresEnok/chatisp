import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppShell } from "@/components/layout/app-shell";
import { OperatorPresence } from "@/features/presence/operator-presence";
import { ContactDocument } from "@/features/contactdocs/contact-document";
import * as presence from "@/api/presence";
import * as documents from "@/api/contactdocs";
import { PanelRequestError } from "@/api/problems";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";
vi.mock("@/api/presence", () => ({ heartbeat: vi.fn(), closeTab: vi.fn(), manual: vi.fn(), occupancy: vi.fn() }));
vi.mock("@/api/contactdocs", () => ({ masked: vi.fn(), reveal: vi.fn(), setDocument: vi.fn(), clearDocument: vi.fn(), findDocument: vi.fn() }));
const principal: PanelPrincipal = { userId: "user-a", tenantId: "tenant-a", membershipId: "member-a", sessionId: "session-a", role: "admin", email: "a@test.invalid", expiresAt: "2030-01-01", mfaEnrollmentRequired: false };
const snapshot = { connected_until: null, last_heartbeat: null, offline_since: null, user_id: "user-a", membership_id: "member-a", state: "available", manual: "available", connected: true, tab_count: 2, assignable: true, tolerance_seconds: 300, offline_for_unassign: false };
beforeEach(() => {
  vi.clearAllMocks(); useSessionStore.getState().applySession(principal, "csrf");
  vi.mocked(presence.heartbeat).mockResolvedValue(snapshot);
  vi.mocked(presence.manual).mockResolvedValue({ ...snapshot, manual: "busy", state: "busy" });
  vi.mocked(presence.closeTab).mockResolvedValue({ ...snapshot, tab_count: 1 });
  vi.mocked(presence.occupancy).mockResolvedValue({ members: [{ membership_id: "member-a", occupied: 3, capacity: 20, open: 2, waiting: 1, snoozed: 0 }] });
  vi.mocked(documents.masked).mockResolvedValue({ contact_id: "contact-a", present: true, mask: "***.***.***-**", kind: "cpf", version: 1 });
});
afterEach(() => vi.useRealTimers());
async function flush() { await act(async () => { await Promise.resolve(); }); }

describe("operações M5", () => {
  it("mantém heartbeat no layout fora de conversas e para imediatamente ao logout", async () => {
    vi.useFakeTimers();
    render(<QueryClientProvider client={new QueryClient()}><MemoryRouter initialEntries={["/configuracoes"]}><AppShell /></MemoryRouter></QueryClientProvider>);
    await flush(); expect(presence.heartbeat).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/Vagas: 3\/20/)).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
    expect(presence.heartbeat).toHaveBeenCalledTimes(3);
    act(() => useSessionStore.getState().markSessionEnded());
    await act(async () => { await vi.advanceTimersByTimeAsync(40_000); });
    expect(presence.heartbeat).toHaveBeenCalledTimes(3);
  });
  it("usa abas distintas e fecha somente a aba desmontada", async () => {
    const first = render(<OperatorPresence />); const second = render(<OperatorPresence />); await flush();
    const tabs = vi.mocked(presence.heartbeat).mock.calls.map(c => c[0]); expect(new Set(tabs).size).toBe(2);
    first.unmount(); await flush(); expect(presence.closeTab).toHaveBeenCalledWith(tabs[0]);
    expect(presence.closeTab).not.toHaveBeenCalledWith(tabs[1]);
    fireEvent.change(second.getByRole("combobox", { name: "Minha presença" }), { target: { value: "busy" } }); await flush();
    expect(presence.manual).toHaveBeenCalledWith("busy", expect.any(AbortSignal));
  });
  it("mantém só máscara e nega revelação sem ecoar o documento nem o erro remoto", async () => {
    vi.mocked(documents.reveal).mockRejectedValue(new PanelRequestError("52998224725", 403));
    render(<ContactDocument principal={principal} contactId="contact-a" />); await flush();
    expect(screen.getByText("***.***.***-**")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("52998224725");
    fireEvent.click(screen.getByRole("button", { name: "Revelar por 15 segundos" })); await flush();
    expect(screen.getByRole("alert")).toHaveTextContent("Acesso negado ao documento.");
    expect(document.body.textContent).not.toContain("52998224725");
  });
  it("não oferece revelar ou alterar para atendente", async () => {
    const agent = { ...principal, role: "agent" }; useSessionStore.getState().applySession(agent, "csrf");
    render(<ContactDocument principal={agent} contactId="contact-a" />); await flush();
    expect(screen.getByText("***.***.***-**")).toBeInTheDocument(); expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(documents.reveal).not.toHaveBeenCalled();
  });
  it("descarta revelação que chega depois da troca de contato e oculta após 15s", async () => {
    vi.useFakeTimers(); let resolve!: (v: Awaited<ReturnType<typeof documents.reveal>>) => void;
    vi.mocked(documents.reveal).mockImplementationOnce(() => new Promise(r => { resolve = r; }));
    const ui = render(<ContactDocument principal={principal} contactId="contact-a" />); await flush();
    fireEvent.click(screen.getByRole("button", { name: "Revelar por 15 segundos" }));
    ui.rerender(<ContactDocument principal={principal} contactId="contact-b" />); await flush();
    await act(async () => resolve({ contact_id: "contact-a", kind: "cpf", document: "52998224725", version: 1 }));
    expect(document.body.textContent).not.toContain("52998224725");
    vi.mocked(documents.reveal).mockResolvedValueOnce({ contact_id: "contact-b", kind: "cpf", document: "52998224725", version: 1 });
    fireEvent.click(screen.getByRole("button", { name: "Revelar por 15 segundos" })); await flush();
    expect(screen.getByText("52998224725")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(document.body.textContent).not.toContain("52998224725");
  });
});
