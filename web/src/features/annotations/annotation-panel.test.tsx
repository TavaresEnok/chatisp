import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { AnnotationPanel } from "./annotation-panel";

afterEach(() => vi.unstubAllGlobals());
it.each(["agent", "supervisor"] as const)("salva nota privada e leitura com UUID válido como %s", async (role) => {
  const calls: { path: string; body: unknown }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (request.method === "POST") {
      const body = await request.json(); calls.push({ path, body });
      // Equivalent to uuid.UnmarshalText used by the real annotation route.
      if (!path.endsWith("/read") && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(body.subject_id)) return new Response('{}', { status: 422 });
      return new Response(JSON.stringify(path.endsWith("/read") ? { ok: true } : { version: 2 }), { headers: { "content-type": "application/json" } });
    }
    const body = path.endsWith("/annotation-tags") ? [] : { priority: "normal", version: 1, read_version: 2, watermark: 3, unread: true, unread_count: 1, tags: [], notes: [], events: [] };
    return new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });
  }));
  const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role, email: "a@b", expiresAt: "2099", mfaEnrollmentRequired: false };
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><AnnotationPanel principal={principal} inbox="i" conversation="c" cursor={8} onChanged={() => {}} /></QueryClientProvider>);
  await user.type(await screen.findByLabelText("Nova nota privada"), "Privado");
  await user.click(screen.getByRole("button", { name: "Salvar nota" }));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0]?.body).toMatchObject({ kind: "note", value: "Privado", request_id: expect.any(String) });
  await waitFor(() => expect(screen.getByLabelText("Nova nota privada")).toHaveValue(""));
  await waitFor(() => expect(screen.getByRole("button", { name: "Marcar como lida" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Marcar como lida" }));
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1]).toMatchObject({ path: expect.stringMatching(/\/annotations\/read$/), body: { watermark: 8, read_version: 2, unread: false } });
  if (role === "supervisor") {
    await waitFor(() => expect(screen.getByRole("button", { name: "Alterar prioridade" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "Alterar prioridade" }));
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[2]?.body).toMatchObject({ kind: "priority", value: "urgent", subject_id: "00000000-0000-0000-0000-000000000000" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Alterar prioridade" })).toBeEnabled());
    expect(screen.queryByText(/inválido/)).not.toBeInTheDocument();
  } else expect(screen.queryByRole("button", { name: "Alterar prioridade" })).not.toBeInTheDocument();
});
