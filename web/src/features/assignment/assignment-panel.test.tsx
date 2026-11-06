import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { AssignmentPanel } from "./assignment-panel";

afterEach(() => vi.unstubAllGlobals());
it("reutiliza a intenção de devolução quando a resposta se perde", async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => { bodies.push(await request.json()); if (bodies.length === 1) throw new TypeError("offline"); return new Response(JSON.stringify({ id: "c" }), { headers: { "content-type": "application/json" } }); }));
  const done = vi.fn();
  const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent" as const, email: "a@b", expiresAt: "2099", mfaEnrollmentRequired: false };
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><AssignmentPanel principal={principal} inbox="i" conversation="c" version={4} onChanged={done} /></QueryClientProvider>);
  await user.click(screen.getByRole("button", { name: "Devolver à fila" }));
  await user.click(screen.getByRole("button", { name: "Confirmar" }));
  expect(await screen.findByText(/Resposta perdida/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Confirmar" }));
  await waitFor(() => expect(done).toHaveBeenCalledWith(false));
  expect(bodies[1]).toEqual(bodies[0]);
  expect(bodies[0]).toMatchObject({ expected_version: 4, client_request_id: expect.any(String) });
});

it("bloqueia destino e cancelamento quando a resposta da transferência se perde", async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "GET" && path.endsWith("/assignment-candidates")) return new Response(JSON.stringify({ candidates: [{ membership_id: "m2", assigned: 0, capacity: 2 }, { membership_id: "m3", assigned: 0, capacity: 2 }] }), { headers: { "content-type": "application/json" } });
    if (request.method === "POST") {
      bodies.push(await request.json());
      if (bodies.length === 1) throw new TypeError("offline");
      return new Response(JSON.stringify({ id: "c" }), { headers: { "content-type": "application/json" } });
    }
    return new Response("{}", { headers: { "content-type": "application/json" } });
  }));
  const done = vi.fn();
  const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent" as const, email: "a@b", expiresAt: "2099", mfaEnrollmentRequired: false };
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><AssignmentPanel principal={principal} inbox="i" conversation="c" version={4} onChanged={done} /></QueryClientProvider>);
  await user.click(screen.getByRole("button", { name: "Transferir para atendente" }));
  await user.selectOptions(await screen.findByLabelText("Atendente"), "m2");
  await user.click(screen.getByRole("button", { name: "Confirmar" }));
  expect(await screen.findByText(/Resposta perdida/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Cancelar" })).toBeDisabled();
  expect(screen.getByLabelText("Atendente")).toBeDisabled();
  expect(screen.getByLabelText("Atendente")).toHaveValue("m2");
  expect(screen.getByRole("button", { name: "Confirmar" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "Confirmar" }));
  await waitFor(() => expect(done).toHaveBeenCalledWith(false));
  expect(bodies[1]).toEqual(bodies[0]);
  expect(bodies[0]).toMatchObject({ expected_version: 4, target_id: "m2", client_request_id: expect.any(String) });
});

it.each(["unmount", "inbox", "conversation", "session"])("ignora conclusão da instância anterior após %s", async change => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(done => { resolve = done; })));
  const done = vi.fn();
  const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent", email: "a@b", expiresAt: "2099", mfaEnrollmentRequired: false };
  const client = new QueryClient();
  const props = { principal, inbox: "i", conversation: "c", version: 4, onChanged: done };
  const ui = (next = props) => <QueryClientProvider client={client}><AssignmentPanel {...next} /></QueryClientProvider>;
  const view = render(ui());
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Devolver à fila" }));
  await user.click(screen.getByRole("button", { name: "Confirmar" }));
  if (change === "unmount") view.unmount();
  else view.rerender(ui({ ...props, inbox: change === "inbox" ? "other" : "i", conversation: change === "conversation" ? "other" : "c", principal: change === "session" ? { ...principal, sessionId: "other" } : principal }));
  await act(async () => { resolve(new Response(JSON.stringify({ id: "c" }), { headers: { "content-type": "application/json" } })); });
  expect(done).not.toHaveBeenCalled();
  if (change !== "unmount") expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  view.unmount(); client.clear();
});

it("permite repetir a leitura de candidatos sem fechar a transferência", async () => {
  let reads = 0;
  vi.stubGlobal("fetch", vi.fn(async () => {
    if (++reads === 1) return new Response(null, { status: 503 });
    return new Response(JSON.stringify({ candidates: [{ membership_id: "m2", assigned: 0, capacity: 2 }] }), { headers: { "content-type": "application/json" } });
  }));
  const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent" as const, email: "a@b", expiresAt: "2099", mfaEnrollmentRequired: false };
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><AssignmentPanel principal={principal} inbox="i" conversation="c" version={4} onChanged={vi.fn()} /></QueryClientProvider>);
  await user.click(screen.getByRole("button", { name: "Transferir para atendente" }));
  await user.click(await screen.findByRole("button", { name: "Tentar novamente candidatos" }));
  expect(await screen.findByRole("option", { name: /Atendente indisponível/ })).toHaveValue("m2");
});
