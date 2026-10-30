import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { CloseConversationDialog } from "@/features/conversation/close-conversation-dialog";

it("envia despedida no encerramento e informa falha sem desfazê-lo", async () => {
  const close = vi.fn().mockResolvedValue({ outcome: "done", farewellFailed: true });
  const farewell = vi.fn();
  const user = userEvent.setup();
  render(<CloseConversationDialog protocol="P1" onClose={close} onFarewell={farewell} onDismiss={() => {}} />);
  await user.type(screen.getByLabelText("Motivo"), "Concluído");
  await user.selectOptions(screen.getByLabelText("Resultado"), "resolved");
  await user.type(screen.getByLabelText("Síntese"), "Resolvido");
  await user.type(screen.getByLabelText("Despedida"), "Até logo");
  await user.click(screen.getByRole("button", { name: "Encerrar" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("despedida não foi enfileirada");
  expect(close).toHaveBeenCalledWith(expect.objectContaining({ farewellBody: "Até logo", farewellRequestId: expect.any(String) }));
  expect(farewell).not.toHaveBeenCalled();
});
