import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchAreaSnapshot } from "@/api/area-snapshot";
import { useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";

vi.mock("@/api/area-snapshot", () => ({
  fetchAreaSnapshot: vi.fn(),
}));

const fetchAreaSnapshotMock = vi.mocked(fetchAreaSnapshot);

describe("estados da área", () => {
  beforeEach(() => {
    fetchAreaSnapshotMock.mockReset();
    useSessionStore.getState().applySession({
      tenantId: "provedor-a", userId: "operador", sessionId: "sessao-a",
      email: "operador@example.test", membershipId: "membro-a", role: "agent",
      expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
    }, "csrf");
  });

  it("mostra carregamento enquanto a consulta não termina", async () => {
    let resolveSnapshot: (value: Awaited<ReturnType<typeof fetchAreaSnapshot>>) => void = () => undefined;
    fetchAreaSnapshotMock.mockReturnValue(new Promise((resolve) => { resolveSnapshot = resolve; }));
    const view = renderPanel("/contatos");
    expect(await screen.findByRole("status")).toHaveTextContent("Carregando.");
    expect(screen.queryByText("backend offline")).not.toBeInTheDocument();
    expect(fetchAreaSnapshotMock).toHaveBeenCalledWith("contacts", "provedor-a");
    view.unmount();
    resolveSnapshot({ tenantId: "provedor-a", area: "contacts", items: [] });
    view.queryClient.clear();
  });

  it("mostra erro sem o texto técnico da falha", async () => {
    fetchAreaSnapshotMock.mockRejectedValue(new Error("backend offline"));
    renderPanel("/contatos");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Não foi possível carregar",
    );
    expect(document.body.textContent ?? "").not.toMatch(/backend offline/);
  });

  it("mostra a lista vazia do provedor em sessão", async () => {
    fetchAreaSnapshotMock.mockResolvedValue({
      tenantId: "provedor-a",
      area: "contacts",
      items: [],
    });
    renderPanel("/contatos");
    expect(
      await screen.findByText("Nenhum contato registrado para este provedor."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Nenhum provedor em sessão/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("list", { name: "Registros da área" }),
    ).not.toBeInTheDocument();
  });
});
