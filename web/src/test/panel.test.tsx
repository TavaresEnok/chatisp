import { act, cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { areas } from "@/lib/areas";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";
import { queryKeys } from "@/lib/query-keys";

const notificationPrincipal = {
  tenantId: "t", userId: "u", sessionId: "s", membershipId: "m1", role: "agent",
  email: "operador@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
};
const m1Preferences = { sound: true, browser: false, silent: false };
const m2Preferences = { sound: false, browser: false, silent: true };
const jsonResponse = (data: unknown) => new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } });
function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => { resolve = done; });
  return { promise, resolve };
}
function switchNotificationMembership(membershipId: string) {
  act(() => useSessionStore.getState().applySession({ ...notificationPrincipal, membershipId }, "csrf"));
}

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
  it("troca preferências por vínculo com cache fresco e descarta salvamento antigo", async () => {
    switchNotificationMembership("m1");
    const reads: string[] = [];
    const writes: { membershipId: string; body: unknown }[] = [];
    const oldSave = deferredResponse();
    let oldSignal: AbortSignal | undefined;
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (!request.url.endsWith("/preferences/notifications")) return jsonResponse({});
      const membershipId = useSessionStore.getState().principal!.membershipId;
      if (request.method === "GET") {
        reads.push(membershipId);
        return jsonResponse(membershipId === "m1" ? m1Preferences : m2Preferences);
      }
      const body = await request.json();
      writes.push({ membershipId, body });
      expect(request.headers.get("X-CSRF-Token")).toBe("csrf");
      if (membershipId === "m1") { oldSignal = request.signal; return oldSave.promise; }
      return jsonResponse(body);
    }));
    const user = userEvent.setup();
    const { queryClient } = renderPanel("/configuracoes");
    expect(queryClient.getDefaultOptions().queries?.staleTime).toBe(30000);
    expect(await screen.findByLabelText("Som")).toBeChecked();
    expect(screen.getByLabelText("Modo silencioso")).not.toBeChecked();
    await user.click(screen.getByLabelText("Notificações do navegador"));
    await waitFor(() => expect(writes).toHaveLength(1));
    switchNotificationMembership("m2");
    expect(screen.queryByLabelText("Som")).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText("Modo silencioso")).toBeChecked());
    expect(screen.getByLabelText("Som")).not.toBeChecked();
    expect(reads).toEqual(["m1", "m2"]);
    expect(oldSignal?.aborted).toBe(true);
    await user.click(screen.getByLabelText("Notificações do navegador"));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual({ membershipId: "m2", body: { ...m2Preferences, browser: true } });
    const currentKey = queryKeys.notificationPreferences({ ...notificationPrincipal, membershipId: "m2" });
    await waitFor(() => expect(queryClient.getQueryData(currentKey)).toEqual({ ...m2Preferences, browser: true }));
    await act(async () => { oldSave.resolve(jsonResponse({ ...m1Preferences, browser: true })); await oldSave.promise; });
    expect(screen.getByLabelText("Som")).not.toBeChecked();
    expect(screen.getByLabelText("Modo silencioso")).toBeChecked();
    expect(queryClient.getQueryData(currentKey)).toEqual({ ...m2Preferences, browser: true });
    expect(queryClient.getQueryData(queryKeys.notificationPreferences(notificationPrincipal))).toEqual(m1Preferences);
    // Navegação comum reutiliza a consulta fresca do mesmo vínculo nas duas páginas.
    await user.click(screen.getByRole("link", { name: "Conversas" }));
    await user.click(screen.getByRole("link", { name: "Configurações" }));
    expect(await screen.findByLabelText("Modo silencioso")).toBeChecked();
    expect(reads).toEqual(["m1", "m2"]);
  });

  it.each(["/configuracoes", "/conversas"])("descarta GET atrasado do vínculo anterior em %s", async (path) => {
    switchNotificationMembership("m1");
    vi.stubGlobal("WebSocket", undefined);
    const oldRead = deferredResponse();
    const reads: string[] = [];
    let oldSignal: AbortSignal | undefined;
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (!request.url.endsWith("/preferences/notifications")) return jsonResponse({ inboxes: [] });
      const membershipId = useSessionStore.getState().principal!.membershipId;
      reads.push(membershipId);
      if (membershipId === "m1") { oldSignal = request.signal; return oldRead.promise; }
      return jsonResponse(m2Preferences);
    }));
    const { queryClient } = renderPanel(path);
    await waitFor(() => expect(reads).toEqual(["m1"]));
    switchNotificationMembership("m2");
    const currentKey = queryKeys.notificationPreferences({ ...notificationPrincipal, membershipId: "m2" });
    await waitFor(() => expect(queryClient.getQueryData(currentKey)).toEqual(m2Preferences));
    expect(reads).toEqual(["m1", "m2"]);
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => { oldRead.resolve(jsonResponse(m1Preferences)); await oldRead.promise; });
    expect(queryClient.getQueryData(currentKey)).toEqual(m2Preferences);
    expect(queryClient.getQueryData(queryKeys.notificationPreferences(notificationPrincipal))).toBeUndefined();
    if (path === "/configuracoes") {
      expect(screen.getByLabelText("Som")).not.toBeChecked();
      expect(screen.getByLabelText("Modo silencioso")).toBeChecked();
    }
  });

  it("conversas consome preferências do vínculo atual e mantém dedup e replay", async () => {
    const sockets: PreferenceSocket[] = [];
    class PreferenceSocket {
      static OPEN = 1;
      readyState = 1;
      onopen: (() => void) | null = null;
      onmessage: ((event: { data: string }) => void) | null = null;
      onclose: (() => void) | null = null;
      send = vi.fn();
      close = vi.fn();
      constructor() { sockets.push(this); }
    }
    const audio = vi.fn(function () { throw new Error("Áudio simulado"); });
    vi.stubGlobal("AudioContext", audio);
    vi.stubGlobal("WebSocket", PreferenceSocket);
    const reads: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.url.endsWith("/preferences/notifications")) {
        const membershipId = useSessionStore.getState().principal!.membershipId;
        reads.push(membershipId);
        return jsonResponse(membershipId === "m1" ? m1Preferences : m2Preferences);
      }
      return jsonResponse(request.url.endsWith("/inboxes") ? { inboxes: [{ id: "i1", name: "Caixa", channel_status: "connected" }] } : { conversations: [] });
    }));
    switchNotificationMembership("m1");
    const user = userEvent.setup();
    const { queryClient } = renderPanel("/conversas");
    await waitFor(() => expect(queryClient.getQueryData(queryKeys.notificationPreferences(notificationPrincipal))).toEqual(m1Preferences));
    await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
    const first = sockets[0]!;
    const emit = (socket: PreferenceSocket, message: unknown) => act(() => socket.onmessage?.({ data: JSON.stringify(message) }));
    const event = (id: string, cursor: number) => ({ type: "events", inbox_id: "i1", events: [{ ID: id, Seq: cursor, Type: "conversation.opened" }] });
    act(() => first.onopen?.());
    emit(first, event("replay", 1));
    expect(audio).not.toHaveBeenCalled();
    emit(first, { type: "snapshot", inbox_id: "i1" });
    emit(first, event("live", 2));
    expect(audio).toHaveBeenCalledTimes(1);
    emit(first, event("live", 2));
    expect(audio).toHaveBeenCalledTimes(1);
    switchNotificationMembership("m2");
    await waitFor(() => expect(queryClient.getQueryData(queryKeys.notificationPreferences({ ...notificationPrincipal, membershipId: "m2" }))).toEqual(m2Preferences));
    await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
    const second = sockets[1]!;
    act(() => second.onopen?.());
    emit(second, { type: "snapshot", inbox_id: "i1" });
    emit(second, event("m2-live", 3));
    emit(first, event("atrasado", 4));
    expect(audio).toHaveBeenCalledTimes(1);
    expect(reads).toEqual(["m1", "m2"]);
    expect(first.close).toHaveBeenCalledOnce();
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
      if (new URL(request.url).pathname.endsWith("/contacts")) return new Response(JSON.stringify({ items: [], next_offset: null }), { headers: { "content-type": "application/json" } });
      if (new URL(request.url).pathname.endsWith("/sectors")) return new Response(JSON.stringify({ sectors: [] }), { headers: { "content-type": "application/json" } });
      if (new URL(request.url).pathname.endsWith("/preferences/notifications")) return new Response(JSON.stringify({ sound: true, browser: false, silent: false }), { headers: { "content-type": "application/json" } });
      if (new URL(request.url).pathname === "/api/panel/branding") {
        return new Response(JSON.stringify({ display_name: "", fallback_name: "Chat ISP", has_logo: false }), { headers: { "content-type": "application/json" } });
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
    for (const area of areas.filter((item) => !["conversations", "contacts", "teams"].includes(item.id))) {
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

  it.each(["/contatos", "/equipes"])("nega acesso anônimo a %s", async (path) => {
    const fetch = vi.fn(); vi.stubGlobal("fetch", fetch); renderPanel(path);
    expect(await screen.findByRole("heading", { name: "Entrar" })).toBeInTheDocument(); expect(fetch).not.toHaveBeenCalled();
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
