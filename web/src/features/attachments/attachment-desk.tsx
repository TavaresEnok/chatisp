import type { MouseEvent } from "react";
import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { cleanupAttachments, downloadAttachment, getAttachment, registerAttachment, uploadAttachment, validateAttachment, type AttachmentRecord } from "@/api/attachments";
import { PanelRequestError } from "@/api/problems";
import { AttachmentPreview } from "@/features/attachments/attachment-preview";
import { forgetAttachmentQueries } from "@/features/attachments/cache";
import { fileError } from "@/features/attachments/media";
import type { PanelPrincipal } from "@/stores/session-store";

type Item = {
  localId: string;
  name: string;
  bytes: number;
  requestId: string;
  file: File;
  attachmentId: string | null;
  phase: "reserving" | "uploading" | "validating" | "validated" | "failed" | "rejected";
  progress: number;
  detail: string;
  record: AttachmentRecord | null;
  downloadUrl: string | null;
};

type Operation = { stamp: number; controller: AbortController };

function denied(error: unknown): boolean {
  return error instanceof PanelRequestError && [403, 404].includes(error.status);
}

function ambiguousStorage(error: unknown): boolean {
  if (!(error instanceof PanelRequestError)) return true;
  return error.status === 0 || error.status === 408 || error.status === 429 || error.status >= 500;
}

function AuthorizedDownload({ name, url, onFailure }: { name: string; url: string; onFailure: (message: string) => void }) {
  async function download(event: MouseEvent<HTMLAnchorElement>) {
    event.preventDefault();
    try {
      const response = await fetch(url, { credentials: "omit", redirect: "follow" });
      if (!response.ok) {
        onFailure("Não foi possível baixar o arquivo autorizado.");
        return;
      }
      const blob = await response.blob();
      const objectUrl = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = objectUrl;
      anchor.download = name;
      anchor.rel = "noopener";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(objectUrl);
    } catch {
      onFailure("Não foi possível baixar o arquivo autorizado.");
    }
  }
  return (
    <a href={url} download={name} rel="noopener noreferrer" referrerPolicy="no-referrer" onClick={(event) => void download(event)}>
      Baixar {name}
    </a>
  );
}

export function AttachmentDesk({ principal, inboxId, conversationId, closed }: { principal: PanelPrincipal; inboxId: string; conversationId: string; closed: boolean }) {
  const client = useQueryClient();
  const [items, setItems] = useState<Item[]>([]);
  const [lookup, setLookup] = useState("");
  const [looked, setLooked] = useState<{ record: AttachmentRecord; url: string | null } | null>(null);
  const [notice, setNotice] = useState("");
  const [blocked, setBlocked] = useState(false);
  const generation = useRef(0);
  const revoked = useRef(false);
  const controllers = useRef(new Set<AbortController>());

  function live(stamp: number) {
    return stamp === generation.current && !revoked.current;
  }

  function track(): Operation | null {
    if (revoked.current) return null;
    const controller = new AbortController();
    controllers.current.add(controller);
    return { stamp: generation.current, controller };
  }

  function release(controller: AbortController) {
    controllers.current.delete(controller);
  }

  function invalidate() {
    generation.current += 1;
    for (const controller of controllers.current) controller.abort();
    controllers.current.clear();
  }

  function revoke() {
    revoked.current = true;
    invalidate();
    setBlocked(true);
    setItems([]);
    setLooked(null);
    forgetAttachmentQueries(client, principal, inboxId, conversationId);
  }

  useEffect(() => () => {
    generation.current += 1;
    for (const controller of controllers.current) controller.abort();
    controllers.current.clear();
  }, []);

  function patch(localId: string, change: Partial<Item>, stamp: number) {
    if (!live(stamp)) return;
    setItems((current) => current.map((item) => (item.localId === localId ? { ...item, ...change } : item)));
  }

  async function advance(item: Item, record: AttachmentRecord, operation: Operation) {
    let current = record;
    if (!live(operation.stamp)) return;
    if (current.state === "failed") {
      patch(item.localId, { phase: "rejected", record: current, detail: "A validação recusou o arquivo. O registro é terminal e os bytes não serão reenviados." }, operation.stamp);
      return;
    }
    if (current.state === "upload") {
      patch(item.localId, { phase: "uploading", record: current, detail: "Enviando bytes." }, operation.stamp);
      await uploadAttachment(inboxId, conversationId, current.id, item.file, (loaded, total) => {
        patch(item.localId, { progress: total > 0 ? Math.round((loaded / total) * 100) : 0 }, operation.stamp);
      }, operation.controller.signal);
      if (!live(operation.stamp)) return;
      patch(item.localId, { phase: "validating", detail: "Validando a assinatura dos bytes." }, operation.stamp);
      current = await validateAttachment(inboxId, conversationId, current.id, operation.controller.signal);
    } else if (current.state === "quarantine") {
      patch(item.localId, { phase: "validating", record: current, detail: "Validando a assinatura dos bytes." }, operation.stamp);
      current = await validateAttachment(inboxId, conversationId, current.id, operation.controller.signal);
    }
    if (!live(operation.stamp)) return;
    if (current.state === "failed") {
      patch(item.localId, { phase: "rejected", record: current, detail: "A validação recusou o arquivo. O registro é terminal e os bytes não serão reenviados." }, operation.stamp);
      return;
    }
    if (current.state === "validated") {
      const ticket = await downloadAttachment(inboxId, conversationId, current.id, operation.controller.signal);
      if (!live(operation.stamp)) return;
      patch(item.localId, { phase: "validated", record: current, downloadUrl: ticket.url, detail: "Arquivo validado." }, operation.stamp);
      return;
    }
    patch(item.localId, { phase: "failed", record: current, detail: "O estado do anexo não permite continuar." }, operation.stamp);
  }

  async function run(item: Item) {
    const operation = track();
    if (!operation) return;
    let attachmentId = item.attachmentId;
    try {
      if (!attachmentId) {
        patch(item.localId, { phase: "reserving", detail: "Reservando o arquivo." }, operation.stamp);
        const reserved = await registerAttachment(inboxId, conversationId, { request_id: item.requestId, name: item.name, bytes: item.bytes }, operation.controller.signal);
        if (!live(operation.stamp)) return;
        attachmentId = reserved.id;
        patch(item.localId, { attachmentId, record: reserved }, operation.stamp);
        await advance(item, reserved, operation);
        return;
      }
      const durable = await getAttachment(inboxId, conversationId, attachmentId, operation.controller.signal);
      if (!live(operation.stamp)) return;
      await advance(item, durable, operation);
    } catch (error) {
      if (!live(operation.stamp)) return;
      if (denied(error)) revoke();
      const detail = error instanceof PanelRequestError
        ? error.message
        : ambiguousStorage(error)
          ? "A resposta do armazenamento não chegou. A próxima tentativa consulta o registro e não reescreve bytes já aceitos."
          : "Falha no armazenamento.";
      if (live(operation.stamp)) patch(item.localId, { phase: "failed", detail }, operation.stamp);
      else setNotice(detail);
    } finally {
      release(operation.controller);
    }
  }

  function accept(files: FileList | File[]) {
    if (revoked.current) return;
    const next: Item[] = [];
    for (const file of files) {
      const error = fileError(file);
      if (error) {
        setNotice(error);
        continue;
      }
      next.push({
        localId: crypto.randomUUID(),
        name: file.name,
        bytes: file.size,
        requestId: crypto.randomUUID(),
        file,
        attachmentId: null,
        phase: "reserving",
        progress: 0,
        detail: "Na fila local de upload.",
        record: null,
        downloadUrl: null,
      });
    }
    setItems((current) => [...current, ...next]);
    for (const item of next) void run(item);
  }

  async function consult() {
    const attachmentId = lookup.trim();
    if (!attachmentId || revoked.current) return;
    const operation = track();
    if (!operation) return;
    setNotice("");
    try {
      const record = await getAttachment(inboxId, conversationId, attachmentId, operation.controller.signal);
      if (!live(operation.stamp)) return;
      setLooked({ record, url: null });
    } catch (error) {
      if (!live(operation.stamp)) return;
      if (denied(error)) revoke();
      setLooked(null);
      setNotice(error instanceof PanelRequestError ? error.message : "Não foi possível consultar o anexo.");
    } finally {
      release(operation.controller);
    }
  }

  async function authorizeDownload(record: AttachmentRecord, localId?: string) {
    if (revoked.current) return;
    const operation = track();
    if (!operation) return;
    try {
      const ticket = await downloadAttachment(inboxId, conversationId, record.id, operation.controller.signal);
      if (!live(operation.stamp)) return;
      if (localId) patch(localId, { downloadUrl: ticket.url }, operation.stamp);
      else setLooked({ record, url: ticket.url });
    } catch (error) {
      if (!live(operation.stamp)) return;
      if (denied(error)) revoke();
      setNotice(error instanceof PanelRequestError ? error.message : "Não foi possível autorizar o download.");
    } finally {
      release(operation.controller);
    }
  }

  async function cleanup() {
    if (revoked.current) return;
    const operation = track();
    if (!operation) return;
    setNotice("");
    try {
      await cleanupAttachments(inboxId, conversationId, operation.controller.signal);
      if (!live(operation.stamp)) return;
      setNotice("Limpeza pedida para registros incompletos vencidos. Validados permanecem.");
    } catch (error) {
      if (!live(operation.stamp)) return;
      if (denied(error)) revoke();
      setNotice(error instanceof PanelRequestError ? error.message : "Não foi possível limpar registros incompletos.");
    } finally {
      release(operation.controller);
    }
  }

  return (
    <section
      aria-label="Anexos da conversa"
      onDragOver={(event) => event.preventDefault()}
      onDrop={(event) => {
        event.preventDefault();
        if (!closed && !blocked) accept(event.dataTransfer.files);
      }}
      onPaste={(event) => {
        if (closed || blocked) return;
        const files = [...event.clipboardData.files];
        if (files.length > 0) {
          event.preventDefault();
          accept(files);
        }
      }}
    >
      <h2>Anexos</h2>
      <p>O arquivo fica no armazenamento privado. O envio como mensagem ainda não está ligado ao canal. Não há ação para despachar este anexo.</p>
      {blocked ? <p>O acesso aos anexos desta conversa foi revogado nesta sessão do painel.</p> : null}
      {closed ? <p>Conversa encerrada: novo upload fica indisponível.</p> : (
        <label>
          Arquivo
          <input
            type="file"
            disabled={blocked}
            onChange={(event) => {
              if (event.target.files) accept(event.target.files);
              event.target.value = "";
            }}
          />
        </label>
      )}
      <ul>
        {items.map((item) => (
          <li key={item.localId}>
            <p>{item.name}</p>
            <p>{item.detail}</p>
            {item.progress > 0 ? <p>Progresso {item.progress}%</p> : null}
            {item.phase === "failed" ? <button type="button" disabled={blocked} onClick={() => void run(item)}>Tentar de novo</button> : null}
            {item.record?.state === "validated" ? <button type="button" disabled={blocked} onClick={() => void authorizeDownload(item.record!, item.localId)}>Obter download autorizado</button> : null}
            {item.downloadUrl ? <AuthorizedDownload name={item.name} url={item.downloadUrl} onFailure={setNotice} /> : null}
            {item.record && item.downloadUrl ? <AttachmentPreview record={item.record} url={item.downloadUrl} /> : null}
          </li>
        ))}
      </ul>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void consult();
        }}
      >
        <label>
          Identificador do anexo
          <input value={lookup} disabled={blocked} onChange={(event) => setLookup(event.target.value)} />
        </label>
        <button type="submit" disabled={blocked}>Consultar anexo</button>
      </form>
      {looked ? (
        <div>
          <p>{looked.record.name} · {looked.record.state} · {looked.record.media_type || "tipo indisponível"}</p>
          {looked.record.state === "validated" ? <button type="button" disabled={blocked} onClick={() => void authorizeDownload(looked.record)}>Obter download autorizado</button> : null}
          {looked.url ? <AuthorizedDownload name={looked.record.name} url={looked.url} onFailure={setNotice} /> : null}
          <AttachmentPreview record={looked.record} url={looked.url} />
        </div>
      ) : null}
      <button type="button" disabled={blocked} onClick={() => void cleanup()}>Limpar registros incompletos vencidos</button>
      {notice ? <p>{notice}</p> : null}
    </section>
  );
}
