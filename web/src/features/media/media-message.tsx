import { useEffect, useState } from "react";
import { headerBlocksPreview, mediaBase, previewKind, type PreviewKind } from "@/features/attachments/media";
import type { MediaTimelineEntry, ReplyTarget } from "@/features/conversation/types";
import { formattedText } from "@/features/conversation/text-format";
import { mapHref, mediaErrorLabel, mediaKindLabel, mediaStatusLabel } from "@/features/media/model";

export type SignedDownload = { url: string; expiresIn: number };
export type DownloadMedia = (attachmentId: string) => Promise<SignedDownload>;

type RemoteView = { identity: string; src: string | null; blocked: boolean; failed: boolean };

export function QuoteButton({ replyTo, onJump }: { replyTo?: ReplyTarget; onJump: (id: string) => void }) {
  if (!replyTo) return null;
  return (
    <button
      type="button"
      className="w-fit rounded-md bg-muted px-2 py-1 text-left text-sm text-foreground"
      onClick={() => onJump(replyTo.id)}
    >
      Mensagem citada: {replyTo.preview}
    </button>
  );
}

export function ReactionList({ reactions }: { reactions?: MediaTimelineEntry["reactions"] }) {
  if (!reactions || reactions.length === 0) return null;
  return (
    <ul aria-label="Reações" className="flex flex-wrap gap-2">
      {reactions.map((reaction) => (
        <li key={`${reaction.externalId}:${reaction.actor}:${reaction.emoji}`} className="rounded-full bg-muted px-2 py-1 text-sm text-foreground">
          {reaction.emoji}
          {reaction.actor ? <span className="text-muted-foreground"> {reaction.actor}</span> : null}
        </li>
      ))}
    </ul>
  );
}

export function MediaMessage({
  entry,
  loadDownload,
  onQuoteJump,
}: {
  entry: MediaTimelineEntry;
  loadDownload?: DownloadMedia;
  onQuoteJump: (id: string) => void;
}) {
  const failure = entry.mediaError ? mediaErrorLabel(entry.mediaError) : null;
  const pending = !failure && entry.mediaStatus ? mediaStatusLabel(entry.mediaStatus) : null;
  return (
    <div className="flex flex-col gap-2" data-media-kind={entry.mediaKind} data-media-error={entry.mediaError || undefined}>
      <QuoteButton replyTo={entry.replyTo} onJump={onQuoteJump} />
      <p className="text-foreground">{mediaKindLabel(entry.mediaKind)}</p>
      {entry.caption ? <p className="whitespace-pre-wrap break-words text-foreground">{formattedText(entry.caption)}</p> : null}
      {failure ? (
        <p role="alert" className="text-sm text-destructive">
          {failure}
        </p>
      ) : null}
      {pending && pending !== "O download da mídia falhou." ? (
        <p role="status" className="text-sm text-foreground">
          {pending}
        </p>
      ) : null}
      {!failure && entry.mediaStatus === "failed" ? (
        <p role="alert" className="text-sm text-destructive">
          O download da mídia falhou.
        </p>
      ) : null}
      {entry.mediaKind === "location" ? <LocationBlock entry={entry} /> : null}
      {entry.mediaKind === "contacts" ? <ContactBlock entry={entry} /> : null}
      {entry.mediaKind === "interactive" || entry.mediaKind === "button" ? <InteractiveBlock entry={entry} /> : null}
      {entry.mediaKind !== "location" && entry.mediaKind !== "contacts" && entry.mediaKind !== "interactive" && entry.mediaKind !== "button" ? (
        <StoredFile entry={entry} loadDownload={loadDownload} blocked={Boolean(failure) || entry.mediaStatus === "failed" || entry.mediaStatus === "pending" || entry.mediaStatus === "reserved"} />
      ) : null}
      <ReactionList reactions={entry.reactions} />
    </div>
  );
}

function LocationBlock({ entry }: { entry: MediaTimelineEntry }) {
  const href = mapHref(entry.latitude, entry.longitude);
  const coordinates =
    entry.latitude !== undefined && entry.longitude !== undefined
      ? `${entry.latitude}, ${entry.longitude}`
      : "";
  return (
    <div className="flex flex-col gap-1">
      {entry.locationName ? <p className="text-foreground">{entry.locationName}</p> : null}
      {entry.address ? <p className="text-foreground">{entry.address}</p> : null}
      {coordinates ? <p className="text-foreground">Coordenadas: {coordinates}</p> : <p className="text-foreground">Coordenadas indisponíveis.</p>}
      {href ? (
        <a href={href} target="_blank" rel="noopener noreferrer" className="w-fit text-sm text-primary underline">
          Abrir no mapa
        </a>
      ) : null}
    </div>
  );
}

function ContactBlock({ entry }: { entry: MediaTimelineEntry }) {
  if (entry.contacts.length === 0) return <p className="text-foreground">Nenhum contato na mensagem.</p>;
  return (
    <ul aria-label="Contatos" className="flex flex-col gap-2">
      {entry.contacts.map((contact, index) => (
        <li key={`${contact.name}:${index}`} className="flex flex-col gap-1">
          <p className="font-medium text-foreground">{contact.name || "Contato sem nome"}</p>
          {contact.org ? <p className="text-foreground">Organização: {contact.org}</p> : null}
          {contact.phones.map((phone) => (
            <p key={phone} className="text-foreground">
              Telefone: {phone}
            </p>
          ))}
          {contact.emails.map((email) => (
            <p key={email} className="text-foreground">
              E-mail: {email}
            </p>
          ))}
          {contact.vcard ? <pre className="whitespace-pre-wrap break-words text-sm text-foreground">{contact.vcard}</pre> : null}
        </li>
      ))}
    </ul>
  );
}

function InteractiveBlock({ entry }: { entry: MediaTimelineEntry }) {
  return (
    <div className="flex flex-col gap-1">
      <p className="text-foreground">Tipo: {entry.interactiveType || "não informado"}</p>
      <p className="text-foreground">Título: {entry.title || "sem título"}</p>
      <p className="text-foreground">Identificador: {entry.replyId || "sem identificador"}</p>
    </div>
  );
}

function StoredFile({
  entry,
  loadDownload,
  blocked,
}: {
  entry: MediaTimelineEntry;
  loadDownload?: DownloadMedia;
  blocked: boolean;
}) {
  const attachment = entry.attachment;
  if (blocked) return null;
  if (!attachment) return <p className="text-sm text-foreground">Arquivo ainda não disponível.</p>;
  if (attachment.state !== "validated" && entry.mediaStatus !== "stored") {
    return <p className="text-sm text-foreground">Arquivo ainda não disponível.</p>;
  }
  const type = attachment.mediaType || entry.mime;
  const kind = previewKind(type);
  const name = entry.filename || attachment.name || mediaKindLabel(entry.mediaKind);
  const player = playerKind(entry, kind);
  if (kind === "refused") return <p className="text-sm text-foreground">HTML e SVG não são exibidos no painel.</p>;
  if (!player) return <FileDownload entry={entry} name={name} type={type} loadDownload={loadDownload} />;
  return <PlayableMedia entry={entry} name={name} type={type} kind={player} loadDownload={loadDownload} />;
}

function playerKind(entry: MediaTimelineEntry, kind: PreviewKind): "image" | "audio" | "video" | null {
  if (kind === "refused" || entry.mediaKind === "document") return null;
  if (entry.mediaKind === "image" || entry.mediaKind === "sticker") return kind === "image" ? "image" : null;
  if (entry.mediaKind === "audio" || entry.mediaKind === "voice") return "audio";
  if (entry.mediaKind === "video") return "video";
  if (kind === "image" || kind === "audio" || kind === "video") return kind;
  return null;
}

function FileDownload({
  entry,
  name,
  type,
  loadDownload,
}: {
  entry: MediaTimelineEntry;
  name: string;
  type: string;
  loadDownload?: DownloadMedia;
}) {
  const [error, setError] = useState("");
  const pdf = mediaBase(type) === "application/pdf" || name.toLowerCase().endsWith(".pdf");
  async function download() {
    if (!entry.attachment || !loadDownload) {
      setError("Não foi possível obter a URL assinada.");
      return;
    }
    setError("");
    try {
      const ticket = await loadDownload(entry.attachment.id);
      const response = await fetch(ticket.url, { credentials: "omit", redirect: "follow" });
      if (!response.ok || headerBlocksPreview(response.headers.get("content-type"))) {
        setError("Não foi possível baixar a mídia.");
        return;
      }
      const bytes = await response.blob();
      const objectUrl = URL.createObjectURL(bytes);
      const anchor = document.createElement("a");
      anchor.href = objectUrl;
      anchor.download = name || "arquivo";
      anchor.rel = "noopener";
      anchor.click();
      URL.revokeObjectURL(objectUrl);
    } catch {
      setError("Não foi possível obter a URL assinada.");
    }
  }
  return (
    <div className="flex flex-col gap-1">
      <p className="text-sm text-foreground">
        {name}
        {type ? ` · ${type}` : ""}
      </p>
      <button type="button" className="h-11 w-fit rounded-lg px-3 text-sm ring-1 ring-foreground/15" onClick={() => void download()}>
        {pdf ? "Baixar PDF" : "Baixar documento"}
      </button>
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}

function PlayableMedia({
  entry,
  name,
  type,
  kind,
  loadDownload,
}: {
  entry: MediaTimelineEntry;
  name: string;
  type: string;
  kind: "image" | "audio" | "video";
  loadDownload?: DownloadMedia;
}) {
  const attachmentId = entry.attachment?.id ?? "";
  const identity = `${attachmentId}\u0000${type}`;
  const [view, setView] = useState<RemoteView>({ identity: "", src: null, blocked: false, failed: false });
  const [open, setOpen] = useState(false);
  const shown = view.identity === identity ? view : { identity, src: null, blocked: false, failed: false };

  useEffect(() => {
    const controller = new AbortController();
    let objectUrl = "";
    const fail = () => {
      if (!controller.signal.aborted) setView({ identity, src: null, blocked: false, failed: true });
    };
    const load = async () => {
      if (!attachmentId || !loadDownload) {
        fail();
        return;
      }
      try {
        const ticket = await loadDownload(attachmentId);
        if (controller.signal.aborted) return;
        const response = await fetch(ticket.url, { credentials: "omit", redirect: "follow", signal: controller.signal });
        if (controller.signal.aborted) return;
        if (headerBlocksPreview(response.headers.get("content-type"))) {
          setView({ identity, src: null, blocked: true, failed: false });
          return;
        }
        if (!response.ok) {
          fail();
          return;
        }
        const bytes = await response.arrayBuffer();
        if (controller.signal.aborted) return;
        objectUrl = URL.createObjectURL(new Blob([bytes], { type: type || "application/octet-stream" }));
        setView({ identity, src: objectUrl, blocked: false, failed: false });
      } catch {
        fail();
      }
    };
    void load();
    return () => {
      controller.abort();
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [attachmentId, identity, loadDownload, type]);

  if (!loadDownload) return <p className="text-sm text-foreground">Download indisponível nesta tela.</p>;
  if (shown.blocked) return <p className="text-sm text-foreground">HTML e SVG não são exibidos no painel.</p>;
  if (shown.failed) {
    return (
      <p role="alert" className="text-sm text-destructive">
        Não foi possível baixar a mídia.
      </p>
    );
  }
  if (!shown.src) return <p className="text-sm text-foreground">Carregando mídia.</p>;
  if (kind === "audio") {
    return <audio controls src={shown.src} aria-label={entry.mediaKind === "voice" || entry.voice ? "Reproduzir nota de voz" : "Reproduzir áudio"} />;
  }
  if (kind === "video") return <video controls src={shown.src} aria-label="Reproduzir vídeo" className="max-h-64" />;
  const alt = entry.mediaKind === "sticker" ? entry.caption || "Figurinha" : entry.caption || name || "Imagem";
  return (
    <div className="flex flex-col gap-2">
      <img alt={alt} src={shown.src} className={open ? "max-h-96 max-w-full" : "max-h-40 max-w-full"} />
      <button type="button" className="h-11 w-fit rounded-lg px-3 text-sm ring-1 ring-foreground/15" onClick={() => setOpen((current) => !current)}>
        {open ? "Fechar imagem" : entry.mediaKind === "sticker" ? "Abrir figurinha" : "Abrir imagem"}
      </button>
    </div>
  );
}
