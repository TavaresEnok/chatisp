import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { renderPanel } from "@/test/render-panel";

const accounts = [
  { label: "Administrador do SaaS", email: "saas.dev@chatisp.local", senha: "senha-saas-sintetica" },
  { label: "Dono da empresa", email: "admin@chatisp.local", senha: "senha-empresa-sintetica" },
  { label: "Atendimento", email: "ana.suporte.demo@chatisp.local", senha: "senha-atendimento-sintetica" },
  { label: "Supervisão", email: "sofia.supervisao.demo@chatisp.local", senha: "senha-supervisao-sintetica" },
];
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); });

it.each([undefined, "false"])("oculta os atalhos sem ativação explícita (%s)", (flag) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", flag);
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_CREDENTIALS", JSON.stringify(accounts));
  renderPanel("/entrar");
  expect(screen.queryByRole("group", { name: "Acessos de desenvolvimento" })).not.toBeInTheDocument();
});

it.each(accounts)("preenche e-mail e senha de $label com um clique sem autenticar", async (account) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", "true");
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_CREDENTIALS", JSON.stringify(accounts));
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup(); renderPanel("/entrar");
  await user.type(screen.getByLabelText("Senha"), "senha-anterior");
  await user.click(screen.getByRole("button", { name: account.label }));
  expect(screen.getByLabelText("E-mail")).toHaveValue(account.email);
  expect(screen.getByLabelText("Senha")).toHaveValue(account.senha);
  expect(screen.getByLabelText("Senha")).toHaveAttribute("type", "password");
  expect(screen.getByLabelText("Senha")).toHaveFocus();
  expect(screen.queryByLabelText(/Importar/)).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
});

it.each([undefined, "invalid", "[]", JSON.stringify([accounts[0], accounts[0], accounts[0], accounts[0]])])("desativa atalhos quando a configuração é inválida", (raw) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", "true");
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_CREDENTIALS", raw);
  renderPanel("/entrar");
  for (const account of accounts) expect(screen.getByRole("button", { name: account.label })).toBeDisabled();
  expect(screen.getByLabelText("Senha")).toHaveValue("");
});
