import { useEffect, useState } from "react";
import type { AttachmentRecord } from "@/api/attachments";
import { headerBlocksPreview, previewKind } from "@/features/attachments/media";

type PreviewView = { identity: string; src: string | null; blocked: boolean; failed: boolean };

export function AttachmentPreview({ record, url }: { record: AttachmentRecord; url: string | null }) {
  const kind = previewKind(record.media_type);
  const identity = `${record.id}\u0000${url ?? ""}`;
  const [view, setView] = useState<PreviewView>({ identity: "", src: null, blocked: false, failed: false });
  const shown = view.identity === identity ? view : { identity, src: null, blocked: false, failed: false };

  useEffect(() => {
    const controller = new AbortController();
    let objectUrl = "";
    const fail = () => {
      if (!controller.signal.aborted) setView({ identity, src: null, blocked: false, failed: true });
    };
    const load = async () => {
      if (!url || record.state !== "validated" || kind === "refused" || kind === "unknown") return;
      try {
        const response = await fetch(url, { credentials: "omit", redirect: "follow", signal: controller.signal });
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
        objectUrl = URL.createObjectURL(new Blob([bytes], { type: record.media_type }));
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
  }, [identity, url, record.id, record.state, record.media_type, kind]);

  if (record.state !== "validated") return <p>Pré-visualização somente depois da validação. Estado atual: {record.state || "desconhecido"}.</p>;
  if (kind === "refused" || shown.blocked) return <p>HTML e SVG não são exibidos no painel.</p>;
  if (kind === "unknown") return <p>Este tipo validado não tem pré-visualização. O download autorizado continua disponível.</p>;
  if (!url) return <p>A pré-visualização deste arquivo aguarda uma URL autorizada.</p>;
  if (shown.failed) return <p>Não foi possível carregar a pré-visualização.</p>;
  if (!shown.src) return <p>Carregando pré-visualização isolada…</p>;
  if (kind === "image") return <img alt={record.name} src={shown.src} />;
  if (kind === "audio") return <audio controls src={shown.src} />;
  if (kind === "video") return <video controls src={shown.src} />;
  return <iframe title={record.name} src={shown.src} sandbox="" referrerPolicy="no-referrer" />;
}
