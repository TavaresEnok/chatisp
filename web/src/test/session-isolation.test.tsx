import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { currentCsrfToken } from "@/api/client";
import { login, rememberSession } from "@/api/panel";
import { queryKeys } from "@/lib/query-keys";
import { useSessionStore, resetSessionStore } from "@/stores/session-store";
import { installPanelOrigin, PanelDouble, startPanelDouble } from "@/test/panel-double";
import { renderAuth } from "@/test/render-auth";

const tenantId = "22222222-2222-4222-8222-222222222222";
const anaId = "11111111-1111-4111-8111-111111111111";
const biaId = "77777777-7777-4777-8777-777777777777";

afterEach(() => {
  document.cookie = "__Host-csrf=; Path=/; Secure; SameSite=Lax; Max-Age=0";
});

function sessionGets(panel: PanelDouble): number {
  return panel.requests.filter(
    (request) => request.method === "GET" && request.path === "/api/panel/session",
  ).length;
}

function setHostCsrf(token: string) {
  document.cookie = `__Host-csrf=${token}; Path=/; Secure; SameSite=Lax`;
}

async function enter(email: string, password: string) {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("E-mail"), email);
  await user.type(screen.getByLabelText("Senha"), password);
  await user.click(screen.getByRole("button", { name: "Entrar" }));
  return user;
}

describe("isolamento da sessão", () => {
  it("troca usuários do mesmo provedor sem perder o CSRF novo", async () => {
    const panel = new PanelDouble();
    const server = await startPanelDouble(panel);
    try {
      await installPanelOrigin(server.origin, async () => {
        const { queryClient } = renderAuth("/entrar");
        await enter("ana@provedor.test", "senha-certa");
        expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
        queryClient.setQueryData(queryKeys.conversations(tenantId), [{ id: "c1", label: "sigilosa" }]);
        useSessionStore.getState().setDisplayName("Ana");
        localStorage.setItem("rascunho", "da ana");
        sessionStorage.setItem("filtro", "abertos");
        expect(await screen.findByText(/Nome nesta aba: Ana/)).toBeInTheDocument();

        const result = await login({ email: "bia@provedor.test", password: "senha-certa" });
        if (!result.data) {
          throw new Error("sessão ausente");
        }
        await act(async () => {
          rememberSession(result.data);
        });

        expect(useSessionStore.getState().principal?.userId).toBe(biaId);
        expect(useSessionStore.getState().principal?.tenantId).toBe(tenantId);
        expect(useSessionStore.getState().tenantId).toBe(tenantId);
        expect(useSessionStore.getState().displayName).toBe("");
        expect(useSessionStore.getState().csrfToken).toBe("csrf-bia");
        expect(currentCsrfToken()).toBe("csrf-bia");
        expect(queryClient.getQueryData(queryKeys.conversations(tenantId))).toBeUndefined();
        expect(localStorage.getItem("rascunho")).toBeNull();
        expect(sessionStorage.getItem("filtro")).toBeNull();
        expect(screen.getByText(/O nome desta aba ainda não foi definido/)).toBeInTheDocument();
        expect(screen.queryByText(/Nome nesta aba: Ana/)).not.toBeInTheDocument();
      });
    } finally {
      await server.close();
    }
  });

  it("restaura pelo cookie de CSRF e rejeita sessão revogada sem repetir a consulta", async () => {
    const panel = new PanelDouble();
    const server = await startPanelDouble(panel);
    try {
      await installPanelOrigin(server.origin, async () => {
        const first = renderAuth("/entrar");
        await enter("ana@provedor.test", "senha-certa");
        expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
        expect(useSessionStore.getState().principal?.userId).toBe(anaId);
        localStorage.setItem("rascunho", "da mesma conta");
        sessionStorage.setItem("filtro", "abertos");
        const beforeReload = sessionGets(panel);

        first.unmount();
        resetSessionStore();
        setHostCsrf("csrf-publico");
        const reloaded = renderAuth("/painel");

        expect(await screen.findByRole("heading", { level: 1, name: "Painel" })).toBeInTheDocument();
        await waitFor(() => {
          expect(useSessionStore.getState().principal?.userId).toBe(anaId);
        });
        expect(useSessionStore.getState().principal?.tenantId).toBe(tenantId);
        expect(currentCsrfToken()).toBe("csrf-publico");
        expect(useSessionStore.getState().csrfToken).toBe("csrf-publico");
        expect(localStorage.getItem("rascunho")).toBe("da mesma conta");
        expect(sessionStorage.getItem("filtro")).toBe("abertos");
        const afterReload = sessionGets(panel);
        expect(afterReload - beforeReload).toBe(1);
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 200));
        });
        expect(sessionGets(panel)).toBe(afterReload);

        panel.revoked = true;
        reloaded.unmount();
        resetSessionStore();
        setHostCsrf("csrf-publico");
        localStorage.setItem("rascunho", "deve sair");
        sessionStorage.setItem("filtro", "ainda");
        const beforeRevoke = sessionGets(panel);
        renderAuth("/painel");

        expect(
          await screen.findByText("Sua sessão expirou ou foi revogada. Entre novamente."),
        ).toBeInTheDocument();
        expect(useSessionStore.getState().principal).toBeNull();
        expect(localStorage.getItem("rascunho")).toBeNull();
        expect(sessionStorage.getItem("filtro")).toBeNull();
        const afterRevoke = sessionGets(panel);
        expect(afterRevoke - beforeRevoke).toBe(1);
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 200));
        });
        expect(sessionGets(panel)).toBe(afterRevoke);
      });
    } finally {
      await server.close();
    }
  });
});
