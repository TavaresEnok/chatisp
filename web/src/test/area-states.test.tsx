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
    useSessionStore.setState({ tenantId: "provedor-a", displayName: "" });
  });

  it("mostra carregamento enquanto a consulta não termina", async () => {
    fetchAreaSnapshotMock.mockReturnValue(new Promise(() => undefined));
    renderPanel("/conversas");
    expect(await screen.findByRole("status")).toHaveTextContent("Carregando.");
    expect(screen.queryByText("backend offline")).not.toBeInTheDocument();
  });

  it("mostra erro sem o texto técnico da falha", async () => {
    fetchAreaSnapshotMock.mockRejectedValue(new Error("backend offline"));
    renderPanel("/conversas");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Não foi possível carregar",
    );
    expect(document.body.textContent ?? "").not.toMatch(/backend offline/);
  });

  it("mostra a lista vazia do provedor em sessão", async () => {
    fetchAreaSnapshotMock.mockResolvedValue({
      tenantId: "provedor-a",
      area: "conversations",
      items: [],
    });
    renderPanel("/conversas");
    expect(
      await screen.findByText("Nenhuma conversa registrada para este provedor."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Nenhum provedor em sessão/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("list", { name: "Registros da área" }),
    ).not.toBeInTheDocument();
  });
});
