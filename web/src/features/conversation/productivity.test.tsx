import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { MessageComposer } from "@/features/conversation/message-composer";
import { formattedText } from "@/features/conversation/text-format";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "u@example.test", membershipId: "m", role: "agent", expiresAt: "2099", mfaEnrollmentRequired: false };
const scope = { tenantId: "t", userId: "u:s", conversationId: "c" };
afterEach(() => { resetSessionStore(); localStorage.clear(); });

it("persiste intenção incerta para replay e limpa ao trocar usuário", async () => {
  const user = userEvent.setup();
  useSessionStore.getState().applySession(principal, "csrf");
  const send = vi.fn().mockResolvedValue({ outcome: "lost" });
  const create = vi.fn(() => "id-estavel");
  const view = render(<MessageComposer scope={scope} onSend={send} createIntentKey={create} />);
  await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "rascunho");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  await screen.findByText(/A resposta do envio não chegou/);
  view.unmount();
  render(<MessageComposer scope={scope} onSend={send} createIntentKey={create} />);
  expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("rascunho");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  expect(send).toHaveBeenLastCalledWith({ intentKey: "id-estavel", text: "rascunho" });
  expect(create).toHaveBeenCalledTimes(1);
  useSessionStore.getState().applySession({ ...principal, userId: "outro" }, "csrf");
  await waitFor(() => expect(localStorage.length).toBe(0));
});

it("insere no cursor e envia por teclado, preservando texto editado durante envio", async () => {
  const user = userEvent.setup();
  let resolve!: (value: { outcome: "accepted" }) => void;
  const send = vi.fn(() => new Promise<{ outcome: "accepted" }>((done) => { resolve = done; }));
  render(<MessageComposer scope={scope} onSend={send} createIntentKey={() => "id"} />);
  const field = screen.getByRole("textbox", { name: "Mensagem" }) as HTMLTextAreaElement;
  await user.type(field, "ola mundo");
  field.setSelectionRange(4, 9);
  await user.click(screen.getByRole("button", { name: "Negrito" }));
  await waitFor(() => expect(field.selectionStart).toBe(5));
  expect(field).toHaveValue("ola *mundo*");
  await user.keyboard("{Control>}{Enter}{/Control}");
  expect(send).toHaveBeenCalledWith({ intentKey: "id", text: "ola *mundo*" });
  await user.type(field, " novo");
  resolve({ outcome: "accepted" });
  await screen.findByText("Mensagem aceita pelo conector.");
  expect(field.value).toContain("novo");
});

it("renderiza marcação como texto seguro", () => {
  const { container } = render(<p>{formattedText("*forte* _suave_ <img src=x>")}</p>);
  expect(container.querySelector("strong")).toHaveTextContent("forte");
  expect(container.querySelector("em")).toHaveTextContent("suave");
  expect(container.querySelector("img")).toBeNull();
  expect(container).toHaveTextContent("<img src=x>");
});

it("após lost, editar e enviar repete a intenção original; nova mensagem exige decisão explícita", async () => {
  const user = userEvent.setup();
  useSessionStore.getState().applySession(principal, "csrf");
  const send = vi.fn().mockResolvedValue({ outcome: "lost" });
  let n = 0;
  render(<MessageComposer scope={scope} onSend={send} createIntentKey={() => `k${++n}`} />);
  const field = screen.getByRole("textbox", { name: "Mensagem" });
  await user.type(field, "original");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  await screen.findByText(/A resposta do envio não chegou/);
  await user.type(field, " editado");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  expect(send).toHaveBeenLastCalledWith({ intentKey: "k1", text: "original" });
  await screen.findByText(/A resposta do envio não chegou/);
  expect(field).toHaveValue("original editado");
  await user.click(screen.getByRole("button", { name: "Enviar como nova mensagem" }));
  expect(send).toHaveBeenLastCalledWith({ intentKey: "k2", text: "original editado" });
});

it("aceitação após desmontagem limpa o rascunho persistido, sem recriar após logout", async () => {
  const user = userEvent.setup();
  useSessionStore.getState().applySession(principal, "csrf");
  let resolve!: (value: { outcome: "accepted" }) => void;
  const send = vi.fn(() => new Promise<{ outcome: "accepted" }>((done) => { resolve = done; }));
  const view = render(<MessageComposer scope={scope} onSend={send} createIntentKey={() => "x"} />);
  await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "aceita");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  view.unmount();
  resolve({ outcome: "accepted" });
  await waitFor(() => expect(localStorage.length).toBe(0));
  render(<MessageComposer scope={scope} onSend={send} createIntentKey={() => "y"} />);
  expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("");
  // logout durante envio: não recria dados
  await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "outra");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  const unmountAndLogout = () => { useSessionStore.getState().clearSession?.(); };
  unmountAndLogout();
  resolve({ outcome: "accepted" });
  await new Promise((r) => setTimeout(r, 10));
  expect(localStorage.length).toBe(0);
});

it("concilia aceitação tardia com compositor remontado e preserva edição nova", async () => {
  const user = userEvent.setup();
  useSessionStore.getState().applySession(principal, "csrf");
  let resolve!: (value: { outcome: "accepted" }) => void;
  const send = vi.fn().mockImplementationOnce(() => new Promise((done) => { resolve = done; })).mockResolvedValue({ outcome: "accepted" });
  let n = 0;
  const props = { scope, onSend: send, createIntentKey: () => `request-${++n}` };
  const view = render(<MessageComposer {...props} />);
  await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "primeira");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  view.unmount();
  render(<MessageComposer {...props} />);
  const field = screen.getByRole("textbox", { name: "Mensagem" });
  await user.clear(field);
  await user.type(field, "nova mensagem");
  await act(async () => { resolve({ outcome: "accepted" }); });
  expect(field).toHaveValue("nova mensagem");
  expect(screen.queryByText(/A tentativa anterior não foi confirmada/)).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  expect(send).toHaveBeenLastCalledWith({ intentKey: "request-2", text: "nova mensagem" });
});
