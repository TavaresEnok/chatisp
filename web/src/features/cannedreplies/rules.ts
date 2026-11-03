export const CLOSED_VARIABLES = ["nome_cliente", "nome_atendente", "protocolo"] as const;
export const SIGNATURE_VARIABLES = ["nome_atendente"] as const;

const SHORTCODE = /^[a-z0-9][a-z0-9_-]{0,63}$/;

export type SignatureView = { enabled: boolean; prefix: string };

export function scanVariables(template: string, allowed: readonly string[]): string | null {
  if (template.includes("<") || template.includes(">") || template.includes("\u0000")) {
    return "O texto não aceita <, > ou caractere nulo.";
  }
  for (let index = 0; index < template.length; ) {
    const char = template[index];
    if (char !== "{" && char !== "}") {
      index += 1;
      continue;
    }
    if (!template.startsWith("{{", index)) return "Chave avulsa não é permitida.";
    const end = template.indexOf("}}", index + 2);
    if (end < 0) return "Variável incompleta.";
    const key = template.slice(index + 2, end);
    if (!allowed.includes(key)) return "Só são permitidas as variáveis nome_cliente, nome_atendente e protocolo.";
    index = end + 2;
  }
  return null;
}

export function replyError(input: { shortcode: string; title: string; content: string }): string | null {
  if (!SHORTCODE.test(input.shortcode)) return "O atalho deve ser minúsculo e ter até 64 caracteres.";
  const title = [...input.title].length;
  const content = [...input.content].length;
  if (title < 1 || title > 120) return "O título precisa ter de 1 a 120 caracteres.";
  if (content < 1 || content > 10000) return "O conteúdo precisa ter de 1 a 10000 caracteres.";
  return scanVariables(input.content, CLOSED_VARIABLES) ?? scanVariables(input.title, []);
}

export function prefixError(prefix: string): string | null {
  const size = [...prefix].length;
  if (size < 1 || size > 240) return "O prefixo precisa ter de 1 a 240 caracteres.";
  const variableError = scanVariables(prefix, SIGNATURE_VARIABLES);
  if (variableError === "Só são permitidas as variáveis nome_cliente, nome_atendente e protocolo.") {
    return "A assinatura só aceita a variável nome_atendente.";
  }
  return variableError;
}

export function slashToken(value: string, cursor: number): { start: number; query: string } | null {
  const safeCursor = Math.max(0, Math.min(cursor, value.length));
  const match = /(^|\s)\/([a-z0-9_-]*)$/.exec(value.slice(0, safeCursor));
  const lead = match?.[1];
  const query = match?.[2];
  if (!match || match.index === undefined || lead === undefined || query === undefined) return null;
  const slash = match.index + lead.length;
  return { start: slash, query };
}

/** Substitui só o token. Não acrescenta assinatura: o servidor faz isso uma vez na aceitação. */
export function insertRendered(value: string, start: number, cursor: number, rendered: string): string {
  return value.slice(0, start) + rendered + value.slice(cursor);
}

export function expandedPrefix(prefix: string, email: string): string {
  return prefix.replaceAll("{{nome_atendente}}", email);
}

export function repeatsServerPrefix(draft: string, signature: SignatureView | null, email: string): boolean {
  if (!signature?.enabled) return false;
  const visible = expandedPrefix(signature.prefix, email);
  return visible.length > 0 && draft.startsWith(visible);
}
