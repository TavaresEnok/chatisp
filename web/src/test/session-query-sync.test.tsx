import { QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { queryKeys } from "@/lib/query-keys";
import { createQueryClient } from "@/lib/query-client";
import { SessionQuerySync } from "@/providers/session-query-sync";
import { useSessionStore } from "@/stores/session-store";

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
});
