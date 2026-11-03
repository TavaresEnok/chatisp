export const TECHNICAL_FILE_BYTES = 32 * 1024 * 1024;

const PREVIEW = {
  "image/jpeg": "image",
  "image/png": "image",
  "image/gif": "image",
  "image/webp": "image",
  "application/pdf": "pdf",
  "audio/mpeg": "audio",
  "audio/wave": "audio",
  "application/ogg": "audio",
  "audio/mp4": "audio",
  "video/mp4": "video",
} as const;

export type PreviewKind = (typeof PREVIEW)[keyof typeof PREVIEW] | "refused" | "unknown";

export function mediaBase(mediaType: string): string {
  return mediaType.toLowerCase().split(";", 1)[0]?.trim() ?? "";
}

export function previewKind(mediaType: string): PreviewKind {
  const type = mediaBase(mediaType);
  if (type.length === 0) return "unknown";
  if (type.includes("svg") || type.includes("html") || type.includes("xml") || type.includes("javascript")) return "refused";
  if (type in PREVIEW) return PREVIEW[type as keyof typeof PREVIEW];
  return "unknown";
}

const FORMAT_MARK = /\p{Cf}/u;

export function nameError(name: string): string | null {
  const bytes = new TextEncoder().encode(name).length;
  if (name.length === 0 || bytes > 180 || name.includes("/") || name.includes("\\") || name === "." || name === ".." || FORMAT_MARK.test(name)) {
    return "O nome do arquivo é inválido.";
  }
  for (const char of name) {
    const code = char.codePointAt(0) ?? 0;
    if (code <= 31 || code === 127 || (code >= 0x80 && code <= 0x9f)) return "O nome do arquivo é inválido.";
  }
  return null;
}

export function fileError(file: { name: string; size: number }): string | null {
  if (!Number.isSafeInteger(file.size) || file.size < 1) return "O arquivo está vazio.";
  if (file.size > TECHNICAL_FILE_BYTES) return "O arquivo passa de 32 MiB, o teto técnico desta entrega.";
  return nameError(file.name);
}

export function headerBlocksPreview(contentType: string | null): boolean {
  return previewKind(contentType ?? "") === "refused";
}
