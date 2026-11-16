import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { renderPanel } from "@/test/render-panel";
import { clearDevelopmentPasswords } from "@/components/auth/development-access";

afterEach(() => { clearDevelopmentPasswords(); vi.unstubAllEnvs(); vi.unstubAllGlobals(); });

it.each([undefined, "false"])("oculta os atalhos sem ativação explícita (%s)", (flag) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", flag);
  renderPanel("/entrar");
  expect(screen.queryByRole("group", { name: "Acessos de desenvolvimento" })).not.toBeInTheDocument();
});

function accessFile(raw: string) {
  const file = new File([raw], "acessos.json", { type: "application/json" });
  // jsdom's File lacks Blob.text; the browser supplies it natively.
  Object.defineProperty(file, "text", { value: async () => raw });
  return file;
}

it("importa as senhas só na aba e preenche o perfil sem enviar o arquivo ou autenticar", async () => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", "true");
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  const view = renderPanel("/entrar");
  await user.upload(screen.getByLabelText("Importar acessos privados (JSON)"), accessFile(JSON.stringify([
    { email: "saas.dev@chatisp.local", senha: "senha-saas-sintetica" },
    { email: "admin@chatisp.local", senha: "senha-empresa-sintetica" },
    { email: "ana.suporte.demo@chatisp.local", senha: "senha-atendimento-sintetica" },
    { email: "sofia.supervisao.demo@chatisp.local", senha: "senha-supervisao-sintetica" },
  ])));
  expect(await screen.findByText(/4 acesso\(s\) carregado\(s\)/)).toBeInTheDocument();
  for (const [label, password] of [["Administrador do SaaS", "senha-saas-sintetica"], ["Dono da empresa", "senha-empresa-sintetica"], ["Atendimento", "senha-atendimento-sintetica"], ["Supervisão", "senha-supervisao-sintetica"]] as const) {
    await user.click(screen.getByRole("button", { name: label }));
    expect(screen.getByLabelText("Senha")).toHaveValue(password);
    expect(screen.getByLabelText("Senha")).toHaveAttribute("type", "password");
  }
  expect(fetch).not.toHaveBeenCalled();
  expect(localStorage.length).toBe(0); expect(sessionStorage.length).toBe(0);
  view.unmount(); renderPanel("/entrar");
  await user.click(screen.getByRole("button", { name: "Atendimento" }));
  expect(screen.getByLabelText("Senha")).toHaveValue("senha-atendimento-sintetica");
  await user.click(screen.getByRole("button", { name: "Limpar senhas carregadas" }));
  await user.click(screen.getByRole("button", { name: "Atendimento" }));
  expect(screen.getByLabelText("Senha")).toHaveValue("");
});

it.each(["não é JSON", '[{"email":"intruso@example.test","senha":"senha-ficticia"}]', '[{"email":"admin@chatisp.local","senha":"a"},{"email":"admin@chatisp.local","senha":"b"}]'])("recusa arquivo inválido sem expor seu conteúdo", async (raw) => {
  vi.stubEnv("VITE_DEVELOPMENT_LOGIN_SHORTCUTS", "true");
  const user = userEvent.setup(); renderPanel("/entrar");
  await user.upload(screen.getByLabelText("Importar acessos privados (JSON)"), accessFile(raw));
  expect(await screen.findByRole("alert")).toHaveTextContent("Arquivo inválido");
  expect(screen.getByLabelText("Senha")).toHaveValue("");
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
