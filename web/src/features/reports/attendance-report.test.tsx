import { screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";

const report = {
  timezone: "America/Sao_Paulo",
  from: "2026-11-03T03:00:00Z",
  to: "2026-11-04T03:00:00Z",
  group: "day",
  definitions: ["Relógios corridos, inclusive fora do expediente."],
  conversations: 1,
  cycles: 1,
  messages: 2,
  first_response: { sample: 0, not_applicable: 1, average_seconds: null, median_seconds: null },
  close_total: { sample: 1, not_applicable: 0, average_seconds: 14400, median_seconds: 14400 },
  close_operational: { sample: 1, not_applicable: 0, average_seconds: 3600, median_seconds: 3600 },
  queue_wait: { sample: 0, not_applicable: 0, average_seconds: null, median_seconds: null },
  results: [{ result: "resolved", author_kind: "bot", label: "Encerrado pelo bot", count: 1 }],
  sectors: [],
  attendants: [],
  inboxes: [],
  reasons: [{ name: "lentidão", count: 1, overlap: false }],
  tags: [{ name: "urgente", count: 1, overlap: true }],
  heatmap: [{ weekday: 2, hour: 8, cycles: 1 }],
  buckets: [{ key: "2026-11-03", cycles: 1, messages: 2, closed: 1, first_response_sample: 0, first_response_average_seconds: null, operational_sample: 1, operational_average_seconds: 3600 }],
  live: { in_queue: 2, in_attendance: 1, waiting_customer: 0, snoozed: 1, unassigned: 2, online_attendants: 3, longest_queue_wait_seconds: 1200, wait_alerts: 1, wait_alert_seconds: 900 },
  csv: "tempos,encerramento_operacional,1,3600,3600,0\n",
};

beforeEach(() => {
  resetSessionStore();
  useSessionStore.getState().applySession({
    tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "supervisor",
    email: "sup@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
  }, "csrf");
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.includes("/api/panel/reports")) {
      return new Response(JSON.stringify(report), { headers: { "content-type": "application/json" } });
    }
    return new Response("{}", { status: 503, headers: { "content-type": "application/problem+json" } });
  }));
});

it("mostra os tempos do período e a situação atual", async () => {
  renderPanel("/relatorios");
  expect(await screen.findByText("Relógios corridos, inclusive fora do expediente.")).toBeInTheDocument();
  expect(screen.getByText("Primeira resposta humana: não aplicável")).toBeInTheDocument();
  expect(screen.getByText("Tempo até o encerramento: 4 h (mediana 4 h, amostra 1)")).toBeInTheDocument();
  expect(screen.getByText("Tempo operacional: 1 h (mediana 1 h, amostra 1)")).toBeInTheDocument();
  expect(screen.getByText("Encerrado pelo bot: 1")).toBeInTheDocument();
  expect(screen.getByText("Estas contagens se sobrepõem e não fecham o total.")).toBeInTheDocument();
  expect(screen.getByText("Alertas de espera: 1")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Baixar CSV" })).toBeInTheDocument();
});

it("destaca a fila e a espera no painel", async () => {
  renderPanel("/painel");
  expect(await screen.findByText("Na fila: 2")).toBeInTheDocument();
  expect(screen.getByText("Sem responsável: 2")).toBeInTheDocument();
  expect(screen.getByText("Alertas de espera: 1")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Baixar CSV" })).not.toBeInTheDocument();
});
