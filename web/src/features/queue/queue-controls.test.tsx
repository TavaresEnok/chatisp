import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { QueueControls } from "./queue-controls";
import type { QueueControlsProps } from "./queue-controls";
import type { QueueFilters, QueueOrder, QueueTab } from "./queue-types";

const emptyFilters: QueueFilters = { status: "", sector: "", assignee: "", inbox: "", tag: "", from: "", to: "" };
const options = {
  status: [{ value: "open", label: "Aberta" }], sector: [{ value: "s1", label: "Suporte" }],
  assignee: [{ value: "a1", label: "Ana" }], inbox: [{ value: "i1", label: "Principal" }],
  tag: [{ value: "t1", label: "Urgente" }],
};

function Harness({ onSelect = vi.fn() }: { onSelect?: (id: string) => void }) {
  const [tab, setTab] = useState<QueueTab>("mine");
  const [search, setSearch] = useState("");
  const [filters, setFilters] = useState(emptyFilters);
  const [order, setOrder] = useState<QueueOrder>("wait");
  return <QueueControls tab={tab} onTabChange={setTab} search={search} onSearchChange={setSearch}
    filters={filters} filterOptions={options} onFiltersChange={setFilters} order={order} onOrderChange={setOrder}
    resultState="ready" items={[{ id: "c1", contactLabel: "Joana", protocol: "123", priorityLabel: "Alta", unreadCount: 2 }]}
    selectedId={null} onSelect={onSelect} />;
}

it("permite navegar por teclado, alterar e limpar a busca e filtros, ordenar e selecionar", async () => {
  const user = userEvent.setup();
  const onSelect = vi.fn();
  render(<Harness onSelect={onSelect} />);
  const mine = screen.getByRole("tab", { name: "Minhas" });
  mine.focus();
  await user.keyboard("{ArrowRight}{End}");
  expect(screen.getByRole("tab", { name: "Todas" })).toHaveFocus();
  expect(screen.getByRole("tab", { name: "Todas" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByText(/disponíveis no seu acesso/)).toBeInTheDocument();
  const search = screen.getByRole("searchbox", { name: "Buscar na fila" });
  await user.type(search, "Joana");
  await user.click(screen.getByRole("button", { name: "Limpar busca" }));
  expect(search).toHaveValue("");
  expect(search).toHaveFocus();
  await user.selectOptions(screen.getByLabelText("Setor"), "s1");
  await user.type(screen.getByLabelText("De"), "2026-01-02");
  await user.selectOptions(screen.getByLabelText("Ordenar por"), "lastMessage");
  expect(screen.getByLabelText("Ordenar por")).toHaveValue("lastMessage");
  await user.click(screen.getByRole("button", { name: "Limpar filtros" }));
  expect(screen.getByLabelText("Setor")).toHaveValue("");
  expect(screen.getByLabelText("De")).toHaveValue("");
  const result = within(screen.getByRole("list", { name: "Conversas encontradas" })).getByRole("button");
  expect(result).toHaveTextContent("Prioridade: Alta");
  expect(result).toHaveTextContent("2 não lida(s)");
  await user.click(result);
  expect(onSelect).toHaveBeenCalledWith("c1");
});

it("apresenta carregamento, erro e lista vazia sem mostrar resultados antigos", () => {
  const props: QueueControlsProps = {
    tab: "mine", onTabChange: vi.fn(), search: "", onSearchChange: vi.fn(), filters: emptyFilters,
    filterOptions: options, onFiltersChange: vi.fn(), order: "wait", onOrderChange: vi.fn(),
    resultState: "loading", items: [{ id: "c1", contactLabel: "Joana", protocol: "123", priorityLabel: null, unreadCount: 0 }],
    selectedId: null, onSelect: vi.fn(),
  };
  const { rerender } = render(<QueueControls {...props} />);
  expect(screen.getByRole("status")).toHaveTextContent("Carregando");
  expect(screen.queryByText("Joana")).not.toBeInTheDocument();
  rerender(<QueueControls {...props} resultState="error" />);
  expect(screen.getByRole("alert")).toHaveTextContent("Não foi possível");
  rerender(<QueueControls {...props} resultState="ready" items={[]} />);
  expect(screen.getByText("Nenhuma conversa encontrada.")).toBeInTheDocument();
});
