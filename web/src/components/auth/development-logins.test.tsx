import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { renderPanel } from "@/test/render-panel";

afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); });

it.each([undefined, "false"])("oculta os atalhos sem ativação explícita (%s)", (flag) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", flag);
  renderPanel("/entrar");
  expect(screen.queryByRole("group", { name: "Acessos de desenvolvimento" })).not.toBeInTheDocument();
});

it.each([
  ["Administrador do SaaS", "saas.dev@chatisp.local"],
  ["Dono da empresa", "admin@chatisp.local"],
  ["Atendimento", "ana.suporte.demo@chatisp.local"],
  ["Supervisão", "sofia.supervisao.demo@chatisp.local"],
] as const)("preenche %s sem autenticar ou reaproveitar a senha", async (label, email) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", "true");
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  renderPanel("/entrar");
  await user.type(screen.getByLabelText("Senha"), "senha-anterior");
  await user.click(screen.getByRole("button", { name: label }));
  expect(screen.getByLabelText("E-mail")).toHaveValue(email);
  expect(screen.getByLabelText("Senha")).toHaveValue("");
  expect(screen.getByLabelText("Senha")).toHaveFocus();
  expect(fetch).not.toHaveBeenCalled();
  expect(screen.getByRole("heading", { name: "Entrar" })).toBeInTheDocument();
});
