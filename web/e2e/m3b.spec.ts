import { expect as playwrightExpect, type Browser, type Page } from "@playwright/test";

const expect = playwrightExpect.configure({ timeout: 15_000 });
import { readFile } from "node:fs/promises";
import { createHmac, randomUUID } from "node:crypto";

type Actor = { Token: string; CSRF: string; User: string; Member: string; Role: string };
type Fixture = {
  App: string; Secret: string; Waba: string; Phone: string;
  M3B: { Tenant: string; Inbox: string; Source: string; Destination: string; Actors: Actor[] };
};
type History = { head: { id: string; version: number; assignee_id: string; status: string; contact_id: string }; next_cursor: number; messages: { body: string; local_state: string }[] };
type Annotations = { unread: boolean; unread_count: number; notes: { body: string }[] };

async function api<T>(page: Page, path: string, csrf: string, method = "GET", body?: unknown): Promise<{ status: number; body: T }> {
  return page.evaluate(async ({ path, csrf, method, body }) => {
    const response = await fetch(path, { method, headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
    return { status: response.status, body: await response.json() };
  }, { path, csrf, method, body });
}
async function session(browser: Browser, actor: Actor, inbox: string) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.addCookies([{ name: "__Host-session", value: actor.Token, url: "https://localhost:5174", secure: true, httpOnly: true, sameSite: "Strict" }]);
  // Headless Chromium keeps an established socket open under Network.emulateNetworkConditions.
  // Closing those sockets is the drop the panel already handles; the server and snapshot stay real.
  await context.addInitScript(() => {
    const native = WebSocket;
    const open: WebSocket[] = [];
    function Socket(this: unknown, url: string | URL, protocols?: string | string[]) {
      const socket = protocols === undefined ? new native(url) : new native(url, protocols);
      open.push(socket);
      return socket;
    }
    Socket.prototype = native.prototype;
    Object.setPrototypeOf(Socket, native);
    Socket.CONNECTING = native.CONNECTING;
    Socket.OPEN = native.OPEN;
    Socket.CLOSING = native.CLOSING;
    Socket.CLOSED = native.CLOSED;
    window.WebSocket = Socket as unknown as typeof WebSocket;
    (window as unknown as { __chatispCloseSockets?: () => void }).__chatispCloseSockets = () => {
      for (const socket of open) if (socket.readyState < WebSocket.CLOSING) socket.close();
    };
  });
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(60_000);
  const frames: string[] = [];
  page.on("websocket", ws => ws.on("framereceived", frame => frames.push(String(frame.payload))));
  // Bootstrap an actual full-assurance fixture session, as in the Go acceptance
  // tests. No route/API response is mocked and production auth stays enabled.
  await page.goto("https://localhost:5174/entrar", { waitUntil: "domcontentloaded", timeout: 60_000 });
  await page.evaluate(async csrf => {
    const response = await fetch("/api/panel/session");
    if (!response.ok) throw new Error(`fixture session: ${response.status}`);
    const data = await response.json();
    const modulePath = "/src/api/panel.ts";
    const { rememberSession } = await import(/* @vite-ignore */ modulePath);
    rememberSession({ ...data, csrf_token: csrf });
    window.history.pushState({}, "", "/conversas");
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, actor.CSRF);
  // The visible label wraps the select, so its full text includes the options.
  // The accessible name stays "Caixa"; match that instead of getByLabel exact.
  await page.getByRole("combobox", { name: "Caixa de atendimento", exact: true }).selectOption(inbox);
  await page.getByRole("tab", { name: "Todas", exact: true }).click();
  await expect.poll(() => frames.some(frame => JSON.parse(frame).type === "snapshot")).toBe(true);
  return { page, context, frames };
}
async function receive(f: Fixture, from: string, text: string) {
  const body = JSON.stringify({ object: "whatsapp_business_account", entry: [{ id: f.Waba, changes: [{ field: "messages", value: { messaging_product: "whatsapp", metadata: { phone_number_id: f.Phone, display_phone_number: f.Phone }, messages: [{ from, id: `wamid.simulation.${randomUUID()}`, timestamp: String(Math.floor(Date.now()/1000)), type: "text", text: { body: text } }] } }] }] });
  const response = await fetch(`http://127.0.0.1:${process.env.CHATISP_E2E_API_PORT ?? "8080"}/api/webhooks/meta?app_id=${encodeURIComponent(f.App)}`, { method: "POST", headers: { "Content-Type": "application/json", "X-Hub-Signature-256": `sha256=${createHmac("sha256",f.Secret).update(body).digest("hex")}` }, body });
  expect(response.status).toBe(200);
}
function rows(page: Page) { return page.getByRole("list", { name: "Conversas encontradas" }).getByRole("button"); }
// Inactive conversations keep a mounted composer under the hidden attribute.
// getByRole ignores that subtree, so the visible draft is the only match.
function composer(page: Page) { return page.getByRole("textbox", { name: "Mensagem", exact: true }); }
async function select(page: Page, protocol: string) { await rows(page).filter({ hasText: protocol }).click(); await expect(composer(page)).toBeVisible(); }

// Read the real QueryClient from the development React provider, without adding
// a product endpoint or exporting sensitive caches to window in application code.
async function cached(page: Page, needle: string) {
  return page.evaluate(needle => {
    const root = document.getElementById("root")!;
    const child = root.firstElementChild as unknown as Record<string, unknown>;
    const key = Object.keys(child).find(k => k.startsWith("__reactFiber$"));
    if (!key) throw new Error("React development fiber unavailable for cache assertion");
    type Fiber = { return?: Fiber; memoizedProps?: { client?: { getQueryCache(): { getAll(): { state: { data: unknown } }[] } } } };
    let fiber = child[key] as Fiber | undefined;
    while (fiber) {
      const client = fiber.memoizedProps?.client;
      if (client?.getQueryCache) return JSON.stringify(client.getQueryCache().getAll().map(q => q.state.data)).includes(needle);
      fiber = fiber.return;
    }
    throw new Error("QueryClientProvider not found");
  }, needle);
}

export async function runM3B(browser: Browser, testInfo: { attach(name: string, options: { body: string; contentType: string }): Promise<void> }) {
  const filename = process.env.M3B_FIXTURE_FILE;
  if (!filename) throw new Error("Fixture ausente: execute npm run test:e2e:m3b; somente localhost");
  const f: Fixture = JSON.parse(await readFile(filename, "utf8"));
  if (!f.M3B || f.M3B.Actors.length !== 3) throw new Error("Fixture M3B inválida");
  const [agent, destination, supervisor] = f.M3B.Actors as [Actor, Actor, Actor];
  const inbox = f.M3B.Inbox;
  const a = await session(browser, agent, inbox);
  const b = await session(browser, destination, inbox);
  const s = await session(browser, supervisor, inbox);
  const frames = a.frames;
  try {
    await receive(f,"5511988887001","m3bsegredo origem recebida");
    await expect(rows(a.page)).toHaveCount(1);
    const label = await rows(a.page).first().innerText();
    const protocol = label.match(/Protocolo\s+(\S+)/)?.[1];
    expect(protocol).toBeTruthy();
    await rows(a.page).first().click();
    await expect(a.page.getByText("m3bsegredo origem recebida", { exact: true })).toBeVisible();
    const snapshot = await api<{ conversations: { id: string; protocol: string }[] }>(a.page,`/api/panel/inboxes/${inbox}/conversations`,agent.CSRF);
    expect(snapshot.status).toBe(200);
    const id = snapshot.body.conversations.find(c => c.protocol === protocol)!.id;
    const item = `/api/panel/inboxes/${inbox}/conversations/${id}`;
    const history = () => api<History>(b.page,`${item}/history`,destination.CSRF);
    const initial = (await history()).body;
    await select(b.page,protocol!);
    await select(s.page,protocol!);

    expect((await api(s.page,`/api/panel/memberships/${agent.Member}/assignment-capacity`,supervisor.CSRF,"POST",{ capacity:1 })).status).toBe(200);
    await a.page.getByRole("button",{ name:"Assumir",exact:true }).click();
    await expect.poll(async () => (await history()).body.head.assignee_id).toBe(agent.Member);
    await a.page.getByLabel("Nova nota privada").fill("nota M3B privada");
    await a.page.getByRole("button",{name:"Salvar nota",exact:true}).click();
    await expect(a.page.getByText(/nota M3B privada —/)).toBeVisible();
    await a.page.getByRole("button",{name:"Marcar como lida",exact:true}).click();
    await expect.poll(async () => (await api<Annotations>(a.page,`${item}/annotations`,agent.CSRF)).body.unread_count).toBe(0);
    expect((await api<Annotations>(b.page,`${item}/annotations`,destination.CSRF)).body.unread_count).toBeGreaterThan(0);

    // Last slot rejects another real inbound, then transfer frees it atomically.
    await receive(f,"5511988887002","capacidade M3B segunda");
    await expect(rows(a.page)).toHaveCount(2);
    const secondSnapshot = await api<{ conversations:{ id:string; protocol:string }[] }>(a.page,`/api/panel/inboxes/${inbox}/conversations`,agent.CSRF);
    const second = secondSnapshot.body.conversations.find(c => c.id !== id)!;
    const secondItem = `/api/panel/inboxes/${inbox}/conversations/${second.id}`;
    const secondHistory = await api<History>(a.page,`${secondItem}/history`,agent.CSRF);
    expect((await api(a.page,`${secondItem}/assume`,agent.CSRF,"POST",{expected_version:secondHistory.body.head.version})).status).toBe(409);

    await a.page.getByRole("button",{name:"Transferir para atendente",exact:true}).click();
    const assignment = a.page.getByRole("dialog",{name:"Confirmar atribuição"});
    await assignment.getByRole("combobox", { name: "Atendente", exact: true }).selectOption(destination.Member);
    await assignment.getByRole("button",{name:"Confirmar",exact:true}).click();
    await expect.poll(async () => (await history()).body.head.assignee_id).toBe(destination.Member);
    await select(a.page,second.protocol);
    await a.page.getByRole("button",{name:"Assumir",exact:true}).click();
    await expect.poll(async () => (await api<History>(a.page,`${secondItem}/history`,agent.CSRF)).body.head.assignee_id).toBe(agent.Member);
    await a.page.getByRole("button",{name:"Devolver à fila",exact:true}).click();
    await assignment.getByRole("button",{name:"Confirmar",exact:true}).click();
    await expect.poll(async () => (await api<History>(a.page,`${secondItem}/history`,agent.CSRF)).body.head.assignee_id).toBe("00000000-0000-0000-0000-000000000000");
    await select(a.page,protocol!);

    // Commit the HTTP send, lose its response, then go offline. The retry must
    // retain the request body/key and produce a single stored outbound message.
    const attempts: unknown[] = [];
    let lose = true;
    await b.page.route(`**${item}/messages`,async route => {
      attempts.push(route.request().postDataJSON());
      if (lose) { lose=false; const response=await route.fetch(); expect(response.status()).toBe(201); await route.abort("failed"); }
      else await route.continue();
    });
    await composer(b.page).fill("resposta M3B teclado");
    await composer(b.page).press("Control+Enter");
    await expect(b.page.getByText(/resposta não chegou|não foi confirmada|não chegou/i).last()).toBeVisible();
    await b.context.setOffline(true);
    await b.page.evaluate(() => (window as unknown as { __chatispCloseSockets?: () => void }).__chatispCloseSockets?.());
    await expect(composer(b.page)).toHaveValue("resposta M3B teclado");
    const draft = () => b.page.evaluate(() => Object.entries(localStorage).filter(([k])=>k.startsWith("chatisp:draft:")));
    const saved = await draft();
    expect(saved.length).toBe(1);
    expect(saved[0]![1]).toContain("intentKey");
    const snapshotsBefore = b.frames.filter(frame=>JSON.parse(frame).type==="snapshot").length;
    await b.context.setOffline(false);
    await expect.poll(()=>b.frames.filter(frame=>JSON.parse(frame).type==="snapshot").length).toBeGreaterThan(snapshotsBefore);
    await expect(b.page.getByText(/Tempo real desconectado/)).toHaveCount(0);
    expect(await draft()).toEqual(saved);
    await composer(b.page).press("Control+Enter");
    await expect.poll(()=>attempts.length).toBe(2);
    expect(attempts[1]).toEqual(attempts[0]);
    await expect.poll(async () => (await history()).body.messages.filter(m=>m.body==="resposta M3B teclado").length).toBe(1);
    await expect.poll(async () => (await history()).body.messages.find(m=>m.body==="resposta M3B teclado")?.local_state).toBe("accepted");
    expect((await history()).body.next_cursor).toBeGreaterThan(initial.next_cursor);
    await expect(composer(b.page)).toHaveValue("");

    // Populate all three real caches before the authorization change.
    expect(await cached(a.page,"m3bsegredo")).toBe(true);
    await b.page.getByRole("button",{name:"Transferir para setor",exact:true}).click();
    const transfer = b.page.getByRole("dialog",{name:"Confirmar atribuição"});
    await transfer.getByRole("combobox", { name: "Setor", exact: true }).selectOption(f.M3B.Destination);
    const boundary = frames.length;
    await transfer.getByRole("button",{name:"Confirmar",exact:true}).click();
    await expect(a.page.getByText("m3bsegredo origem recebida",{exact:true})).toHaveCount(0);
    await expect(a.page.getByText(/nota M3B privada —/)).toHaveCount(0);
    await expect.poll(()=>cached(a.page,"m3bsegredo")).toBe(false);
    await expect.poll(()=>cached(a.page,"nota M3B privada")).toBe(false);
    const denied = await api(a.page,`${item}/history`,agent.CSRF);
    expect(denied.status).toBe(403);
    expect(JSON.stringify(denied.body)).not.toContain("m3bsegredo");
    const hidden = await api<{ items: { id:string }[] }>(a.page,`/api/panel/queue?inbox=${inbox}&tab=all&search=m3bsegredo`,agent.CSRF);
    expect(hidden.status).toBe(200); expect(hidden.body.items).toEqual([]);
    await a.page.getByLabel("Buscar na fila").fill("m3bsegredo");
    await expect(rows(a.page)).toHaveCount(0);
    const events = await api<{events:{aggregate_id:string}[];next_cursor:number}>(a.page,`/api/panel/inboxes/${inbox}/events?after=0`,agent.CSRF);
    expect(events.status).toBe(200);
    expect(events.body.events.some(e => e.aggregate_id === id)).toBe(false);
    expect(events.body.next_cursor).toBeGreaterThan(0);
    expect(JSON.stringify(events.body)).not.toContain("m3bsegredo");
    await select(b.page,protocol!);
    await expect(b.page.getByText("m3bsegredo origem recebida",{exact:true})).toBeVisible();
    await expect(s.page.getByText("m3bsegredo origem recebida",{exact:true})).toBeVisible();
    await b.page.getByRole("button",{name:"Assumir",exact:true}).click();
    await expect.poll(async ()=>(await history()).body.head.assignee_id).toBe(destination.Member);
    await b.page.getByRole("button",{name:"Encerrar com motivo",exact:true}).click();
    const close = b.page.getByRole("dialog",{name:"Encerrar com motivo"});
    await close.getByLabel("Motivo",{exact:true}).fill("Aceite M3B");
    await close.getByLabel("Resultado",{exact:true}).selectOption("resolved");
    await close.getByLabel("Síntese",{exact:true}).fill("Fluxo integrado demonstrado");
    await close.getByRole("button",{name:/Encerrar/,exact:true}).click();
    await expect.poll(async ()=>(await history()).body.head.status).toBe("closed");
    await expect(b.page.getByText("Atendimento encerrado.",{exact:true})).toBeVisible();
    // Events after transfer and close must not restore the old user's content.
    expect(frames.slice(boundary).join("\n")).not.toContain("m3bsegredo");
    expect(await cached(a.page,"m3bsegredo")).toBe(false);
    await testInfo.attach("m3b-evidence",{body:JSON.stringify({browser:browser.version(),contexts:3,sectors:2,protocol,initialCursor:initial.next_cursor,finalCursor:(await history()).body.next_cursor,replayedRequestIdentical:true,revokedCache:true}),contentType:"application/json"});
  } finally { await Promise.all([a.context.close(),b.context.close(),s.context.close()]); }
}
