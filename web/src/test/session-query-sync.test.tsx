import { QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { currentCsrfToken } from "@/api/client";
import { queryKeys } from "@/lib/query-keys";
import { createQueryClient } from "@/lib/query-client";
import { SessionQuerySync } from "@/providers/session-query-sync";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

const tenantId = "22222222-2222-4222-8222-222222222222";

function principal(userId: string, sessionId: string): PanelPrincipal {
  return {
    userId,
    email: `${userId}@provedor.test`,
    tenantId,
    membershipId: "33333333-3333-4333-8333-333333333333",
    role: "admin",
    sessionId,
    expiresAt: "2026-12-01T12:00:00.000Z",
    mfaEnrollmentRequired: false,
  };
}

describe("troca de provedor", () => {
  it("apaga a consulta do provedor anterior", () => {
    const queryClient = createQueryClient();
    queryClient.setQueryData(queryKeys.conversations("provedor-a"), [
      { id: "c1", label: "não deve permanecer" },
    ]);
    useSessionStore.setState({ tenantId: "provedor-a", displayName: "Ana" });

    render(
      <QueryClientProvider client={queryClient}>
        <SessionQuerySync />
      </QueryClientProvider>,
    );

    expect(queryClient.getQueryData(queryKeys.conversations("provedor-a"))).toEqual([
      { id: "c1", label: "não deve permanecer" },
    ]);

    act(() => {
      useSessionStore.getState().setTenantId("provedor-b");
    });

    expect(
      queryClient.getQueryData(queryKeys.conversations("provedor-a")),
    ).toBeUndefined();
    expect(useSessionStore.getState().displayName).toBe("");
  });

  it("apaga consulta, armazenamento e nome ao trocar o usuário do mesmo provedor", () => {
    const queryClient = createQueryClient();
    useSessionStore.getState().applySession(principal("user-a", "session-a"), "csrf-a");
    useSessionStore.getState().setDisplayName("Ana");
    queryClient.setQueryData(queryKeys.conversations(tenantId), [
      { id: "c1", label: "sigilosa" },
    ]);
    localStorage.setItem("rascunho", "da ana");
    sessionStorage.setItem("filtro", "abertos");

    render(
      <QueryClientProvider client={queryClient}>
        <SessionQuerySync />
      </QueryClientProvider>,
    );

    act(() => {
      useSessionStore.getState().applySession(principal("user-b", "session-b"), "csrf-b");
    });

    expect(queryClient.getQueryData(queryKeys.conversations(tenantId))).toBeUndefined();
    expect(localStorage.getItem("rascunho")).toBeNull();
    expect(sessionStorage.getItem("filtro")).toBeNull();
    expect(useSessionStore.getState().displayName).toBe("");
    expect(useSessionStore.getState().principal?.userId).toBe("user-b");
    expect(useSessionStore.getState().tenantId).toBe(tenantId);
    expect(useSessionStore.getState().csrfToken).toBe("csrf-b");
    expect(currentCsrfToken()).toBe("csrf-b");
  });

  it("mantém consulta, armazenamento e nome ao renovar o mesmo principal", () => {
    const queryClient = createQueryClient();
    useSessionStore.getState().applySession(principal("user-a", "session-a"), "csrf-a");
    useSessionStore.getState().setDisplayName("Ana");
    queryClient.setQueryData(queryKeys.conversations(tenantId), [
      { id: "c1", label: "sigilosa" },
    ]);
    localStorage.setItem("rascunho", "da ana");

    render(
      <QueryClientProvider client={queryClient}>
        <SessionQuerySync />
      </QueryClientProvider>,
    );

    act(() => {
      useSessionStore.getState().applySession(principal("user-a", "session-renovada"), "csrf-novo");
    });

    expect(queryClient.getQueryData(queryKeys.conversations(tenantId))).toEqual([
      { id: "c1", label: "sigilosa" },
    ]);
    expect(localStorage.getItem("rascunho")).toBe("da ana");
    expect(useSessionStore.getState().displayName).toBe("Ana");
    expect(useSessionStore.getState().principal?.sessionId).toBe("session-renovada");
    expect(currentCsrfToken()).toBe("csrf-novo");
  });
});
