import type { components } from "@/api/schema";

export class PanelRequestError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "PanelRequestError";
    this.status = status;
  }
}

export const MFA_CODE_REQUIRED = "informe o código de autenticação";
export const TENANT_REQUIRED = "selecione o provedor";

function isProblem(error: unknown): error is components["schemas"]["Problem"] {
  return typeof error === "object" && error !== null && "title" in error && "status" in error;
}

export function problemMessage(error: unknown, status: number): string {
  if (isProblem(error)) {
    const field = error.errors?.find((item) => item.message.length > 0)?.message;
    if (field) {
      return field;
    }
    if (error.detail && error.detail.trim().length > 0) {
      return error.detail;
    }
    if (error.title.trim().length > 0) {
      return error.title;
    }
  }
  if (status === 401) {
    return "Sua sessão expirou ou foi revogada.";
  }
  if (status === 403) {
    return "Acesso negado.";
  }
  return "Não foi possível concluir. Tente de novo.";
}

export function errorMessage(error: unknown): string {
  if (error instanceof PanelRequestError) {
    return error.message;
  }
  return "Não foi possível concluir. Tente de novo.";
}
