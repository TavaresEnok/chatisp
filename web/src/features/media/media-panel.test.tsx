import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Schema } from "@/api/attendance";
import { timeline } from "@/features/conversation/attendance-adapter";
import { MessageTimeline } from "@/features/conversation/message-timeline";
import type { MediaTimelineEntry } from "@/features/conversation/types";
import { MediaMessage, type DownloadMedia } from "@/features/media/media-message";
import { deliverOutboundMedia } from "@/features/media/send-media";
import { registerAttachment, uploadAttachment, validateAttachment } from "@/api/attachments";
import { AttendanceError, send } from "@/api/attendance";

vi.mock("@/api/attachments", () => ({
  registerAttachment: vi.fn(),
  uploadAttachment: vi.fn(),
  validateAttachment: vi.fn(),
}));

vi.mock("@/api/attendance", async () => {
  const actual = await vi.importActual<typeof import("@/api/attendance")>("@/api/attendance");
  return { ...actual, send: vi.fn() };
});

const replyId = "11111111-1111-4111-8111-111111111111";

function attachment(id: string, mediaType: string, name: string) {
  return { id, name, mediaType, state: "validated", bytes: 4 };
}

function entry(partial: Partial<MediaTimelineEntry> & Pick<MediaTimelineEntry, "id" | "mediaKind">): MediaTimelineEntry {
  return {
    kind: "media",
    author: "Cliente",
    caption: "",
    filename: "",
    mime: "",
    voice: partial.mediaKind === "voice",
    locationName: "",
    address: "",
    contacts: [],
    interactiveType: "",
    replyId: "",
    title: "",
    mediaStatus: "stored",
    mediaError: "",
    ...partial,
  };
}

function message(partial: Partial<Schema["Message"]> & Pick<Schema["Message"], "id" | "body">): Schema["Message"] {
  return {
    conversation_id: "conversa",
    direction: "inbound",
    external_message_id: partial.id,
    historical: false,
    ingested_at: "2026-01-01T00:00:00Z",
    local_state: "accepted",
    origin: "customer",
    trusted_at: "2026-01-01T00:00:00Z",
    visibility: "public",
    ...partial,
  };
}

describe("painel de mídia", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("mostra imagem, documento, áudio, nota de voz, vídeo, figurinha, mapa, vCard, resposta e falhas", async () => {
    const user = userEvent.setup();
    const createObjectURL = vi.fn(() => "blob:midia");
    vi.spyOn(URL, "createObjectURL").mockImplementation(createObjectURL);
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    const fetchMock = vi.fn(async () => new Response(new Uint8Array([1, 2, 3]), { headers: { "content-type": "image/jpeg" } }));
    vi.stubGlobal("fetch", fetchMock);
    const loadDownload = vi.fn<DownloadMedia>(async (id) => ({ url: `https://arquivos.test/${id}`, expiresIn: 60 }));
    const kinds: MediaTimelineEntry[] = [
      entry({ id: "img", mediaKind: "image", caption: "foto da fachada", mime: "image/jpeg", attachment: attachment("img-file", "image/jpeg", "fachada.jpg") }),
      entry({ id: "pdf", mediaKind: "document", filename: "boleto.pdf", mime: "application/pdf", attachment: attachment("pdf-file", "application/pdf", "boleto.pdf") }),
      entry({ id: "txt", mediaKind: "document", filename: "notas.txt", mime: "text/plain", attachment: attachment("txt-file", "text/plain", "notas.txt") }),
      entry({ id: "audio", mediaKind: "audio", mime: "audio/mpeg", attachment: attachment("audio-file", "audio/mpeg", "audio.mp3") }),
      entry({ id: "voice", mediaKind: "voice", mime: "audio/ogg", voice: true, attachment: attachment("voice-file", "audio/ogg", "nota.ogg") }),
      entry({ id: "video", mediaKind: "video", mime: "video/mp4", attachment: attachment("video-file", "video/mp4", "video.mp4") }),
      entry({ id: "sticker", mediaKind: "sticker", mime: "image/webp", attachment: attachment("sticker-file", "image/webp", "figurinha.webp") }),
      entry({ id: "place", mediaKind: "location", latitude: -23.5, longitude: -46.6, locationName: "Sede", address: "Rua 1", mediaStatus: "" }),
      entry({
        id: "card",
        mediaKind: "contacts",
        mediaStatus: "",
        contacts: [{ name: "Bia", phones: ["+5511988887777"], emails: [], org: "ISP", vcard: "BEGIN:VCARD\nVERSION:3.0\nFN:Bia\nTEL:+5511988887777\nEND:VCARD" }],
      }),
      entry({ id: "choice", mediaKind: "interactive", interactiveType: "button_reply", replyId: "yes", title: "Sim", mediaStatus: "" }),
      entry({ id: "button", mediaKind: "button", interactiveType: "button", replyId: "ok", title: "Ok", mediaStatus: "" }),
      entry({ id: "mime", mediaKind: "image", mediaStatus: "failed", mediaError: "mime_nao_reconhecido" }),
      entry({ id: "size", mediaKind: "document", mediaStatus: "failed", mediaError: "tamanho_excedido" }),
      entry({ id: "limit", mediaKind: "video", mediaStatus: "failed", mediaError: "limite_excedido" }),
      entry({ id: "down", mediaKind: "audio", mediaStatus: "failed", mediaError: "download_falhou" }),
      entry({
        id: "react",
        mediaKind: "image",
        caption: "com reação",
        mime: "image/jpeg",
        attachment: attachment("react-file", "image/jpeg", "reacao.jpg"),
        reactions: [{ emoji: "👍", actor: "5511999990001", externalId: "wamid.react" }],
      }),
    ];

    render(
      <div>
        {kinds.map((item) => (
          <MediaMessage key={item.id} entry={item} loadDownload={loadDownload} onQuoteJump={() => undefined} />
        ))}
      </div>,
    );

    expect(await screen.findByRole("img", { name: "foto da fachada" })).toBeInTheDocument();
    const openImage = screen.getAllByRole("button", { name: "Abrir imagem" })[0];
    if (!openImage) throw new Error("botão de abrir imagem ausente");
    await user.click(openImage);
    expect(screen.getByRole("button", { name: "Fechar imagem" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Baixar PDF" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Baixar documento" })).toBeInTheDocument();
    expect(await screen.findByLabelText("Reproduzir áudio")).toBeInTheDocument();
    expect(await screen.findByLabelText("Reproduzir nota de voz")).toBeInTheDocument();
    expect(await screen.findByLabelText("Reproduzir vídeo")).toBeInTheDocument();
    expect(await screen.findByRole("img", { name: "Figurinha" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Abrir figurinha" }));
    expect(screen.getByText("Sede")).toBeInTheDocument();
    expect(screen.getByText("Rua 1")).toBeInTheDocument();
    expect(screen.getByText("Coordenadas: -23.5, -46.6")).toBeInTheDocument();
    const map = screen.getByRole("link", { name: "Abrir no mapa" });
    expect(map).toHaveAttribute("href", "https://www.openstreetmap.org/?mlat=-23.5&mlon=-46.6#map=16/-23.5/-46.6");
    expect(map).toHaveAttribute("target", "_blank");
    expect(map).toHaveAttribute("rel", "noopener noreferrer");
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.getByText("Bia")).toBeInTheDocument();
    expect(screen.getByText("Telefone: +5511988887777")).toBeInTheDocument();
    expect(screen.getByText(/BEGIN:VCARD/)).toBeInTheDocument();
    expect(screen.getByText("Título: Sim")).toBeInTheDocument();
    expect(screen.getByText("Identificador: yes")).toBeInTheDocument();
    expect(screen.getByText("Título: Ok")).toBeInTheDocument();
    expect(screen.getByText("O tipo do arquivo não foi reconhecido.")).toBeInTheDocument();
    expect(screen.getByText("O arquivo passa do limite permitido para este tipo.")).toBeInTheDocument();
    expect(screen.getByText("O limite de armazenamento foi atingido.")).toBeInTheDocument();
    expect(screen.getByText("Não foi possível baixar a mídia do canal.")).toBeInTheDocument();
    const reactions = screen.getByRole("list", { name: "Reações" });
    expect(reactions).toHaveTextContent("👍");
    expect(reactions).toHaveTextContent("5511999990001");

    await user.click(screen.getByRole("button", { name: "Baixar PDF" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("https://arquivos.test/pdf-file", expect.objectContaining({ credentials: "omit" })));
    expect(createObjectURL).toHaveBeenCalled();

    loadDownload.mockRejectedValueOnce(new Error("ticket"));
    await user.click(screen.getByRole("button", { name: "Baixar documento" }));
    expect(await screen.findByText("Não foi possível obter a URL assinada.")).toBeInTheDocument();
  });

  it("rola até a mensagem citada e mostra a reação no alvo", async () => {
    const user = userEvent.setup();
    const prototype = HTMLElement.prototype;
    const height = Object.getOwnPropertyDescriptor(prototype, "clientHeight");
    const width = Object.getOwnPropertyDescriptor(prototype, "clientWidth");
    Object.defineProperty(prototype, "clientHeight", { configurable: true, get: () => 480 });
    Object.defineProperty(prototype, "clientWidth", { configurable: true, get: () => 320 });
    const quoted = message({ id: "origem", body: "texto citado", content_kind: "text" });
    const reply = message({
      id: "resposta",
      body: "",
      content_kind: "image",
      trusted_at: "2026-01-01T00:01:00Z",
      reply_to_message_id: "origem",
      content: { type: "image", caption: "foto citada", media_status: "stored", mime: "image/jpeg" },
      attachment: { id: "att", name: "foto.jpg", media_type: "image/jpeg", state: "validated", bytes: 4 },
      reactions: [{ emoji: "❤️", external_id: "wamid.react", actor: "5511" }],
    });
    const entries = timeline({
      head: {
        id: "conversa",
        protocol: "p",
        contact_id: "c",
        assignee_id: "a",
        controller: "human",
        controller_epoch: 1,
        created_at: "2026-01-01T00:00:00Z",
        cycle_number: 1,
        last_customer_message_at: "2026-01-01T00:00:00Z",
        status: "open",
        summary: "",
        version: 1,
        waiting_since: "2026-01-01T00:00:00Z",
      },
      messages: [quoted, reply],
      timeline: [],
      next_cursor: 0,
    });
    expect(entries[1]).toMatchObject({ kind: "media", mediaKind: "image", replyTo: { id: "origem", preview: "texto citado" } });

    render(
      <MessageTimeline
        scope={{ tenantId: "t", userId: "u", conversationId: "conversa" }}
        protocol="p"
        status="ready"
        entries={entries}
        onReply={() => undefined}
      />,
    );
    expect(within(screen.getByRole("listitem", { name: /foto citada/ })).getByRole("list", { name: "Reações" })).toHaveTextContent("❤️");
    await user.click(screen.getByRole("button", { name: "Mensagem citada: texto citado" }));
    await waitFor(() => expect(document.activeElement).toHaveAttribute("id", "mensagem-origem"));

    if (height) Object.defineProperty(prototype, "clientHeight", height);
    else Reflect.deleteProperty(prototype, "clientHeight");
    if (width) Object.defineProperty(prototype, "clientWidth", width);
    else Reflect.deleteProperty(prototype, "clientWidth");
  });

  it("envia a mídia com legenda e reply_to e expõe a recusa da conversão", async () => {
    const file = new File([Uint8Array.of(1, 2, 3, 4)], "nota-de-voz.webm", { type: "audio/webm" });
    const record = { id: "att-1", name: file.name, media_type: "audio/webm", state: "upload", bytes: 4, expires_at: "2026-01-01T00:00:00Z" };
    vi.mocked(registerAttachment).mockResolvedValue(record);
    vi.mocked(uploadAttachment).mockResolvedValue(undefined);
    vi.mocked(validateAttachment).mockResolvedValue({ ...record, state: "validated", media_type: "audio/webm" });
    vi.mocked(send).mockResolvedValueOnce({ state: "accepted", attempt_id: "a", idempotency_id: "k", message_id: "m", replayed: false });

    const accepted = await deliverOutboundMedia({
      inboxId: "caixa",
      conversationId: "conversa",
      version: 3,
      command: { intentKey: "chave-midia", caption: "legenda da nota", file, replyToMessageId: replyId },
    });
    expect(accepted).toEqual({ outcome: "accepted" });
    expect(send).toHaveBeenCalledWith("caixa", "conversa", {
      expected_version: 3,
      client_request_id: "chave-midia",
      body: "legenda da nota",
      attachment_id: "att-1",
      reply_to_message_id: replyId,
    });

    vi.mocked(send).mockRejectedValueOnce(new AttendanceError(422, "ffmpeg não encontrado; conversão recusada; arquivo original preservado"));
    const missing = await deliverOutboundMedia({
      inboxId: "caixa",
      conversationId: "conversa",
      version: 3,
      command: { intentKey: "chave-2", caption: "", file },
    });
    expect(missing).toEqual({ outcome: "failed", detail: "ffmpeg não encontrado; conversão recusada; arquivo original preservado" });

    vi.mocked(send).mockRejectedValueOnce(new AttendanceError(422, "ffmpeg falhou; conversão recusada; arquivo original preservado"));
    const failed = await deliverOutboundMedia({
      inboxId: "caixa",
      conversationId: "conversa",
      version: 3,
      command: { intentKey: "chave-3", caption: "", file },
    });
    expect(failed).toEqual({ outcome: "failed", detail: "ffmpeg falhou; conversão recusada; arquivo original preservado" });

    vi.mocked(send).mockRejectedValueOnce(new AttendanceError(409, "A conversa mudou."));
    const conflict = await deliverOutboundMedia({
      inboxId: "caixa",
      conversationId: "conversa",
      version: 3,
      command: { intentKey: "chave-4", caption: "", file },
    });
    expect(conflict).toEqual({ outcome: "failed", detail: "A conversa mudou.", conflict: true });
  });

  it("baixa XML validado como arquivo e não o renderiza", async () => {
    const user = userEvent.setup();
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:xml");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    const fetchMock = vi.fn(async (url: string) => new Response("<note/>", {
      headers: { "content-type": String(url).endsWith("app-file") ? "application/xml" : "text/xml" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const loadDownload = vi.fn<DownloadMedia>(async (id) => ({ url: `https://arquivos.test/${id}`, expiresIn: 60 }));
    render(
      <div>
        <MediaMessage
          entry={entry({ id: "xml-text", mediaKind: "document", filename: "nota.xml", mime: "text/xml", attachment: attachment("text-file", "text/xml", "nota.xml") })}
          loadDownload={loadDownload}
          onQuoteJump={() => undefined}
        />
        <MediaMessage
          entry={entry({ id: "xml-app", mediaKind: "document", filename: "pedido.xml", mime: "application/xml", attachment: attachment("app-file", "application/xml", "pedido.xml") })}
          loadDownload={loadDownload}
          onQuoteJump={() => undefined}
        />
      </div>,
    );

    expect(screen.queryByText(/HTML e SVG não são exibidos/)).not.toBeInTheDocument();
    expect(document.querySelector("iframe, object, embed")).toBeNull();
    const buttons = screen.getAllByRole("button", { name: "Baixar documento" });
    expect(buttons).toHaveLength(2);
    const [textXml, applicationXml] = buttons;
    if (!textXml || !applicationXml) throw new Error("botões de XML ausentes");
    await user.click(textXml);
    await user.click(applicationXml);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock).toHaveBeenCalledWith("https://arquivos.test/text-file", expect.objectContaining({ credentials: "omit" }));
    expect(fetchMock).toHaveBeenCalledWith("https://arquivos.test/app-file", expect.objectContaining({ credentials: "omit" }));
    expect(screen.queryByText("Não foi possível baixar a mídia.")).not.toBeInTheDocument();
  });
});
