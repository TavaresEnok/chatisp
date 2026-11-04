import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AttachmentPreview } from "@/features/attachments/attachment-preview";
import { AttachmentDesk } from "@/features/attachments/attachment-desk";
import { attachmentKey } from "@/features/attachments/cache";
import { fileError, previewKind } from "@/features/attachments/media";
import { PanelRequestError } from "@/api/problems";
import type { PanelPrincipal } from "@/stores/session-store";

vi.mock("@/api/attachments", () => ({
  registerAttachment: vi.fn(),
  uploadAttachment: vi.fn(),
  validateAttachment: vi.fn(),
  downloadAttachment: vi.fn(),
  getAttachment: vi.fn(),
  cleanupAttachments: vi.fn(),
}));

import { downloadAttachment, getAttachment, registerAttachment, uploadAttachment, validateAttachment } from "@/api/attachments";

const principal: PanelPrincipal = {
  tenantId: "tenant-a", userId: "user-a", sessionId: "sess-a", email: "ana@provedor.test",
  membershipId: "mem-a", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
};
const record = { id: "att-1", name: "foto.jpg", bytes: 3, expires_at: "2099-01-01T00:00:00Z", media_type: "image/jpeg", state: "validated" };

describe("anexos", () => {
  beforeEach(() => {
    vi.stubGlobal("URL", { createObjectURL: () => "blob:preview", revokeObjectURL: () => undefined });
    vi.mocked(registerAttachment).mockReset();
    vi.mocked(uploadAttachment).mockReset();
    vi.mocked(validateAttachment).mockReset();
    vi.mocked(downloadAttachment).mockReset();
    vi.mocked(getAttachment).mockReset();
  });

  it("recusa HTML e SVG e só mostra mídia validada", async () => {
    expect(previewKind("image/svg+xml")).toBe("refused");
    expect(previewKind("text/html")).toBe("refused");
    expect(previewKind("text/xml")).toBe("unknown");
    expect(previewKind("application/xml")).toBe("unknown");
    expect(previewKind("text/xml; charset=utf-8")).toBe("unknown");
    expect(fileError({ name: "../segredo.svg", size: 10 })).toMatch(/inválido/);
    expect(fileError({ name: "foto\u200b.jpg", size: 10 })).toMatch(/inválido/);
    render(<AttachmentPreview record={{ ...record, media_type: "text/html", name: "pagina.html" }} url="https://arquivos.test/html" />);
    expect(screen.getByText(/HTML e SVG não são exibidos/)).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(document.querySelector("iframe, object, embed")).toBeNull();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array([1, 2, 3]), { headers: { "content-type": "image/jpeg" } })));
    render(<AttachmentPreview record={record} url="https://arquivos.test/foto" />);
    expect(await screen.findByRole("img", { name: "foto.jpg" })).toHaveAttribute("src", "blob:preview");
  });

  it("mostra progresso e falha, reutiliza a reserva e não oferece envio", async () => {
    const user = userEvent.setup();
    vi.mocked(registerAttachment).mockResolvedValue({ ...record, state: "upload" });
    vi.mocked(uploadAttachment).mockImplementation(async (_inbox, _conversation, _id, _file, onProgress) => {
      onProgress(1, 2);
      throw new PanelRequestError("Falha de armazenamento.", 503);
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><AttachmentDesk principal={principal} inboxId="inbox-a" conversationId="conv-a" closed={false} /></QueryClientProvider>);
    expect(screen.getByText(/Não há ação para despachar este anexo/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /enviar anexo|enviar mídia|enviar arquivo/i })).not.toBeInTheDocument();
    const file = new File([new Uint8Array([1, 2, 3])], "foto.jpg", { type: "image/jpeg" });
    await user.upload(screen.getByLabelText("Arquivo"), file);
    expect(await screen.findByText("Falha de armazenamento.")).toBeInTheDocument();
    expect(screen.getByText("Progresso 50%")).toBeInTheDocument();
    vi.mocked(getAttachment).mockResolvedValue({ ...record, state: "upload" });
    await user.click(screen.getByRole("button", { name: "Tentar de novo" }));
    expect(registerAttachment).toHaveBeenCalledTimes(1);
    expect(uploadAttachment).toHaveBeenCalledTimes(2);
    expect(vi.mocked(registerAttachment).mock.calls[0]?.[2]?.request_id).toEqual(expect.any(String));
    client.setQueryData(attachmentKey(principal, "inbox-a", "conv-a", "att-1"), record);
    vi.mocked(getAttachment).mockRejectedValue(new PanelRequestError("Acesso negado.", 403));
    await user.type(screen.getByLabelText("Identificador do anexo"), "att-1");
    await user.click(screen.getByRole("button", { name: "Consultar anexo" }));
    await screen.findByText("Acesso negado.");
    expect(client.getQueryData(attachmentKey(principal, "inbox-a", "conv-a", "att-1"))).toBeUndefined();
    expect(validateAttachment).not.toHaveBeenCalled();
  });

  it("retoma validação sem repetir o upload aceito ou ambíguo", async () => {
    const user = userEvent.setup();
    const file = new File([new Uint8Array([1, 2, 3])], "foto.jpg", { type: "image/jpeg" });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.mocked(registerAttachment).mockResolvedValue({ ...record, state: "upload" });
    vi.mocked(uploadAttachment).mockResolvedValue(undefined);
    vi.mocked(validateAttachment).mockRejectedValueOnce(new PanelRequestError("Validação indisponível.", 503)).mockResolvedValue({ ...record, state: "validated" });
    vi.mocked(getAttachment).mockResolvedValue({ ...record, state: "quarantine" });
    vi.mocked(downloadAttachment).mockResolvedValue({ url: "https://arquivos.test/foto", expires_in: 30 });
    render(<QueryClientProvider client={client}><AttachmentDesk principal={principal} inboxId="inbox-a" conversationId="conv-a" closed={false} /></QueryClientProvider>);
    await user.upload(screen.getByLabelText("Arquivo"), file);
    expect(await screen.findByText("Validação indisponível.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Tentar de novo" }));
    expect(await screen.findByRole("link", { name: "Baixar foto.jpg" })).toHaveAttribute("href", "https://arquivos.test/foto");
    expect(uploadAttachment).toHaveBeenCalledTimes(1);
    expect(validateAttachment).toHaveBeenCalledTimes(2);
    expect(getAttachment).toHaveBeenCalled();

    vi.mocked(uploadAttachment).mockReset();
    vi.mocked(uploadAttachment).mockRejectedValue(new Error("rede"));
    vi.mocked(registerAttachment).mockResolvedValue({ ...record, id: "att-2", state: "upload" });
    vi.mocked(getAttachment).mockResolvedValue({ ...record, id: "att-2", state: "quarantine" });
    vi.mocked(validateAttachment).mockResolvedValue({ ...record, id: "att-2", state: "validated" });
    await user.upload(screen.getByLabelText("Arquivo"), file);
    expect(await screen.findByText(/não reescreve bytes já aceitos/)).toBeInTheDocument();
    expect(uploadAttachment).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Tentar de novo" }));
    expect(uploadAttachment).toHaveBeenCalledTimes(1);
    expect(validateAttachment).toHaveBeenCalledWith("inbox-a", "conv-a", "att-2", expect.any(AbortSignal));
  });

  it("baixa o arquivo quando a pré-visualização falha ou não existe", async () => {
    const user = userEvent.setup();
    const createObjectURL = vi.fn(() => "blob:arquivo");
    vi.stubGlobal("URL", { createObjectURL, revokeObjectURL: vi.fn() });
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new Error("preview"))
      .mockResolvedValueOnce(new Response(new Uint8Array([4, 5, 6]), { headers: { "content-type": "image/jpeg" } }));
    vi.stubGlobal("fetch", fetchMock);
    vi.mocked(getAttachment).mockResolvedValue(record);
    vi.mocked(downloadAttachment).mockResolvedValue({ url: "https://arquivos.test/foto", expires_in: 30 });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><AttachmentDesk principal={principal} inboxId="inbox-a" conversationId="conv-a" closed={false} /></QueryClientProvider>);
    await user.type(screen.getByLabelText("Identificador do anexo"), "att-1");
    await user.click(screen.getByRole("button", { name: "Consultar anexo" }));
    await user.click(screen.getByRole("button", { name: "Obter download autorizado" }));
    const link = await screen.findByRole("link", { name: "Baixar foto.jpg" });
    expect(link).toHaveAttribute("href", "https://arquivos.test/foto");
    expect(await screen.findByText("Não foi possível carregar a pré-visualização.")).toBeInTheDocument();
    await user.click(link);
    expect(fetchMock).toHaveBeenLastCalledWith("https://arquivos.test/foto", expect.objectContaining({ credentials: "omit" }));
    expect(createObjectURL).toHaveBeenCalled();

    vi.mocked(getAttachment).mockResolvedValue({ ...record, id: "att-zip", name: "anexo.zip", media_type: "application/zip" });
    vi.mocked(downloadAttachment).mockResolvedValue({ url: "https://arquivos.test/zip", expires_in: 30 });
    fetchMock.mockResolvedValue(new Response(new Uint8Array([7]), { headers: { "content-type": "application/zip" } }));
    await user.clear(screen.getByLabelText("Identificador do anexo"));
    await user.type(screen.getByLabelText("Identificador do anexo"), "att-zip");
    await user.click(screen.getByRole("button", { name: "Consultar anexo" }));
    expect(await screen.findByText(/não tem pré-visualização/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Obter download autorizado" }));
    const zip = await screen.findByRole("link", { name: "Baixar anexo.zip" });
    expect(zip).toHaveAttribute("href", "https://arquivos.test/zip");
    await user.click(zip);
    expect(fetchMock).toHaveBeenLastCalledWith("https://arquivos.test/zip", expect.objectContaining({ credentials: "omit" }));
  });

  it("reinicia a pré-visualização ao falhar e ao trocar de anexo", async () => {
    let blob = 0;
    vi.stubGlobal("URL", {
      createObjectURL: () => `blob:preview-${++blob}`,
      revokeObjectURL: () => undefined,
    });
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 500 }))
      .mockResolvedValueOnce(new Response(new Uint8Array([1]), { headers: { "content-type": "image/jpeg" } }))
      .mockResolvedValueOnce(new Response(new Uint8Array([2]), { headers: { "content-type": "image/png" } })));
    const first = { ...record, id: "att-1", name: "foto.jpg", media_type: "image/jpeg" };
    const second = { ...record, id: "att-2", name: "outra.png", media_type: "image/png" };
    const view = render(<AttachmentPreview record={first} url="https://arquivos.test/falha" />);
    expect(await screen.findByText("Não foi possível carregar a pré-visualização.")).toBeInTheDocument();
    view.rerender(<AttachmentPreview record={first} url="https://arquivos.test/ok" />);
    expect(await screen.findByRole("img", { name: "foto.jpg" })).toHaveAttribute("src", "blob:preview-1");
    expect(screen.queryByText("Não foi possível carregar a pré-visualização.")).not.toBeInTheDocument();
    view.rerender(<AttachmentPreview record={second} url={null} />);
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByText("A pré-visualização deste arquivo aguarda uma URL autorizada.")).toBeInTheDocument();
    view.rerender(<AttachmentPreview record={second} url="https://arquivos.test/png" />);
    expect(await screen.findByRole("img", { name: "outra.png" })).toHaveAttribute("src", "blob:preview-2");
    expect(screen.queryByRole("img", { name: "foto.jpg" })).not.toBeInTheDocument();
  });

  it("não repõe registro, link nem pré-visualização quando o sucesso chega depois da revogação", async () => {
    const user = userEvent.setup();
    let resolveDownload: (value: { url: string; expires_in: number }) => void = () => undefined;
    const downloadGate = new Promise<{ url: string; expires_in: number }>((resolve) => {
      resolveDownload = resolve;
    });
    vi.mocked(getAttachment)
      .mockResolvedValueOnce(record)
      .mockRejectedValueOnce(new PanelRequestError("Acesso negado.", 403));
    vi.mocked(downloadAttachment).mockReturnValue(downloadGate);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><AttachmentDesk principal={principal} inboxId="inbox-a" conversationId="conv-a" closed={false} /></QueryClientProvider>);
    await user.type(screen.getByLabelText("Identificador do anexo"), "att-1");
    await user.click(screen.getByRole("button", { name: "Consultar anexo" }));
    await user.click(await screen.findByRole("button", { name: "Obter download autorizado" }));
    await user.click(screen.getByRole("button", { name: "Consultar anexo" }));
    expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
    resolveDownload({ url: "https://arquivos.test/tardia", expires_in: 30 });
    await act(async () => {
      await downloadGate;
    });
    expect(screen.queryByRole("link", { name: "Baixar foto.jpg" })).not.toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "foto.jpg" })).not.toBeInTheDocument();
    expect(screen.queryByText(/foto\.jpg/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Consultar anexo" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Limpar registros incompletos vencidos" })).toBeDisabled();
  });
});
