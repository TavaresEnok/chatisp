import { api, currentCsrfToken } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";
import type { components } from "@/api/schema";

export type AttachmentRecord = components["schemas"]["AttachmentRecord"];
export type DownloadTicket = components["schemas"]["DownloadOutputBody"];

function unwrap<T>(result: { data?: T; error?: unknown; response: Response }, emptyOk = false): T {
  if (result.response.ok && (emptyOk || result.data !== undefined)) {
    return result.data as T;
  }
  throw new PanelRequestError(problemMessage(result.error, result.response.status), result.response.status);
}

export async function registerAttachment(inboxId: string, conversationId: string, body: { request_id: string; name: string; bytes: number }, signal?: AbortSignal): Promise<AttachmentRecord> {
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments", {
    params: { path: { inbox_id: inboxId, conversation_id: conversationId } },
    body,
    signal,
  }));
}

export function uploadAttachment(inboxId: string, conversationId: string, attachmentId: string, file: Blob, onProgress: (loaded: number, total: number) => void, signal?: AbortSignal): Promise<void> {
  const path = `/api/panel/inboxes/${encodeURIComponent(inboxId)}/conversations/${encodeURIComponent(conversationId)}/attachments/${encodeURIComponent(attachmentId)}/content`;
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new PanelRequestError("O envio do arquivo foi interrompido.", 0));
      return;
    }
    const xhr = new XMLHttpRequest();
    const onAbort = () => xhr.abort();
    signal?.addEventListener("abort", onAbort);
    const finish = () => signal?.removeEventListener("abort", onAbort);
    xhr.open("PUT", path);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    const csrf = currentCsrfToken();
    if (csrf) xhr.setRequestHeader("X-CSRF-Token", csrf);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(event.loaded, event.total);
    };
    xhr.onload = () => {
      finish();
      if (xhr.status === 204) {
        resolve();
        return;
      }
      reject(new PanelRequestError(xhr.status === 0 ? "Não foi possível enviar o arquivo." : "O envio do arquivo foi recusado.", xhr.status));
    };
    xhr.onerror = () => {
      finish();
      reject(new PanelRequestError("Não foi possível enviar o arquivo.", 0));
    };
    xhr.onabort = () => {
      finish();
      reject(new PanelRequestError("O envio do arquivo foi interrompido.", 0));
    };
    xhr.send(file);
  });
}

export async function validateAttachment(inboxId: string, conversationId: string, attachmentId: string, signal?: AbortSignal): Promise<AttachmentRecord> {
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments/{attachment_id}/validate", {
    params: { path: { inbox_id: inboxId, conversation_id: conversationId, attachment_id: attachmentId } },
    signal,
  }));
}

export async function getAttachment(inboxId: string, conversationId: string, attachmentId: string, signal?: AbortSignal): Promise<AttachmentRecord> {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments/{attachment_id}", {
    params: { path: { inbox_id: inboxId, conversation_id: conversationId, attachment_id: attachmentId } },
    signal,
  }));
}

export async function downloadAttachment(inboxId: string, conversationId: string, attachmentId: string, signal?: AbortSignal): Promise<DownloadTicket> {
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments/{attachment_id}/download", {
    params: { path: { inbox_id: inboxId, conversation_id: conversationId, attachment_id: attachmentId } },
    signal,
  }));
}

export async function cleanupAttachments(inboxId: string, conversationId: string, signal?: AbortSignal): Promise<void> {
  unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments/cleanup", {
    params: { path: { inbox_id: inboxId, conversation_id: conversationId } },
    signal,
  }), true);
}
