import { describe, expect, it } from "vitest";
import { quoteTarget } from "@/features/media/model";

const messages = [{ id: "m-1", body: "Olá, tudo bem?" }];

describe("quoteTarget", () => {
  it("ignora o UUID zero que a API usa para 'sem citação'", () => {
    expect(quoteTarget("00000000-0000-0000-0000-000000000000", undefined, messages)).toBeUndefined();
    expect(quoteTarget(undefined, { reply_to_message_id: "00000000-0000-0000-0000-000000000000" }, messages)).toBeUndefined();
    expect(quoteTarget(undefined, undefined, messages)).toBeUndefined();
  });
  it("mostra a mensagem citada ou avisa quando ela não está no histórico", () => {
    expect(quoteTarget("m-1", undefined, messages)?.preview).toContain("Olá");
    expect(quoteTarget("m-404", undefined, messages)?.preview).toBe("Mensagem citada indisponível");
  });
});
