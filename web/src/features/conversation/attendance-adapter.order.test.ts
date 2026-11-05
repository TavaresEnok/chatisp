import { describe, expect, it } from "vitest";
import { timeline } from "@/features/conversation/attendance-adapter";
import type { Schema } from "@/api/attendance";

function message(id: string, trusted: string | null, ingested: string): Schema["Message"] {
  return { id, direction: "inbound", body: id, local_state: "", trusted_at: trusted, ingested_at: ingested, content_kind: "text" } as unknown as Schema["Message"];
}

describe("timeline order", () => {
  it("ordena pelo horário do driver, usa a ingestão sem ele e preserva a ordem do servidor no empate", () => {
    const history = {
      timeline: [],
      messages: [
        // Ingestadas nesta ordem; a "a" chegou atrasada, mas o driver a marcou antes.
        message("b", "2026-11-04T12:00:10Z", "2026-11-04T12:00:11Z"),
        message("a", "2026-11-04T12:00:05Z", "2026-11-04T12:00:12Z"),
        message("c", null, "2026-11-04T12:00:20Z"),
        message("d", "2026-11-04T12:00:30Z", "2026-11-04T12:00:31Z"),
        message("e", "2026-11-04T12:00:30Z", "2026-11-04T12:00:32Z"),
      ],
    } as unknown as Schema["HistoryOutputBody"];
    expect(timeline(history).map((entry) => entry.id)).toEqual(["a", "b", "c", "d", "e"]);
  });
});
