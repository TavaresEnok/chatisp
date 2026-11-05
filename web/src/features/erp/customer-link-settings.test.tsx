import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { CustomerLinkSettings } from "@/features/erp/customer-link-settings";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function view() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><CustomerLinkSettings scope="sess-a" /></QueryClientProvider>);
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("grava o modelo com a versão lida e mostra a recusa do servidor", async () => {
  const sent: unknown[] = [];
  let stored = { url_template: "", version: 0, placeholder: "{cliente}" };
  vi.stubGlobal("fetch", vi.fn(async (input: Request | string) => {
    const request = typeof input === "string" ? new Request(input) : input;
    if (request.method === "PUT") {
      const body = await request.json() as { url_template: string; version: number };
      sent.push(body);
      if (body.url_template.includes("token")) {
        return json({ title: "o link não pode carregar credencial nem documento", detail: "o link não pode carregar credencial nem documento", status: 422 }, 422);
      }
      stored = { url_template: body.url_template, version: body.version + 1, placeholder: "{cliente}" };
      return json(stored);
    }
    return json(stored);
  }));
  const user = userEvent.setup();
  view();
  const field = await screen.findByLabelText("Modelo do link");
  expect(screen.queryByRole("button", { name: "Remover link" })).not.toBeInTheDocument();
  await user.type(field, "https://erp.exemplo/c/{{cliente}?token=1");
  await user.click(screen.getByRole("button", { name: "Salvar link" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("credencial nem documento");
  await user.clear(field);
  await user.type(field, "https://erp.exemplo/c/{{cliente}");
  await user.click(screen.getByRole("button", { name: "Salvar link" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Link salvo.");
  expect(sent).toEqual([
    { url_template: "https://erp.exemplo/c/{cliente}?token=1", version: 0 },
    { url_template: "https://erp.exemplo/c/{cliente}", version: 0 },
  ]);
  await user.click(screen.getByRole("button", { name: "Remover link" }));
  expect(await screen.findByText("Link removido.")).toBeInTheDocument();
  expect(sent[2]).toEqual({ url_template: "", version: 1 });
});
