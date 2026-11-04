import { render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { components } from "@/api/schema";
import { queueItem } from "./queue-adapter";
import { QueueControls } from "./queue-controls";
import type { QueueFilters } from "./queue-types";

type ApiItem = components["schemas"]["RealtimeStoreQueueItem"];

const filters: QueueFilters = { status: "", sector: "", assignee: "", inbox: "", tag: "", from: "", to: "" };
const options = {
  status: [], sector: [], assignee: [], inbox: [], tag: [],
};

function apiItem(id: string, extra: Partial<ApiItem> = {}): ApiItem {
  return {
    id, inbox_id: "i", sector_id: "s", assignee_id: null, contact_id: "ct", contact_label: id,
    protocol: id, status: "open", priority: "normal", unread: false, unread_count: 0,
    wait_since: "2020-01-01T00:00:00Z", last_message_at: "2026-01-01T00:00:00Z", ...extra,
    wait_alert: extra.wait_alert === true,
  };
}

it("destaca só a conversa que a fila marcou e não inventa o prazo", () => {
  const items = [
    queueItem(apiItem("atrasada", { contact_label: "Maria", protocol: "A-1", wait_alert: true })),
    queueItem(apiItem("no-prazo", { contact_label: "João", protocol: "A-2", wait_alert: false })),
    queueItem(apiItem("sem-sinal", { contact_label: "Ana", protocol: "A-3", priority: "urgent" })),
  ];
  render(<QueueControls tab="all" onTabChange={vi.fn()} search="" onSearchChange={vi.fn()} filters={filters}
    filterOptions={options} onFiltersChange={vi.fn()} order="wait" onOrderChange={vi.fn()} resultState="ready"
    items={items} selectedId={null} onSelect={vi.fn()} />);
  const buttons = within(screen.getByRole("list", { name: "Conversas encontradas" })).getAllByRole("button");
  const first = buttons[0];
  const second = buttons[1];
  const third = buttons[2];
  if (!first || !second || !third) {
    throw new Error("fila incompleta");
  }
  expect(first).toHaveTextContent("Passou do tempo de espera.");
  expect(first).toHaveAttribute("data-wait-alert", "true");
  expect(first.className).toContain("bg-destructive/10");
  expect(second).not.toHaveTextContent("Passou do tempo de espera.");
  expect(second).not.toHaveAttribute("data-wait-alert");
  expect(third).toHaveTextContent("Prioridade: Urgente");
  expect(third).not.toHaveTextContent("Passou do tempo de espera.");
  expect(screen.getAllByText("Passou do tempo de espera.")).toHaveLength(1);
});
