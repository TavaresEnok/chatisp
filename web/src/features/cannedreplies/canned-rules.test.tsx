import { describe, expect, it } from "vitest";
import { insertRendered, prefixError, repeatsServerPrefix, replyError, slashToken } from "@/features/cannedreplies/rules";

describe("regras das respostas", () => {
  it("aceita só as três variáveis e rejeita chave avulsa", () => {
    expect(replyError({ shortcode: "ola", title: "Olá", content: "Oi {{nome_cliente}} {{protocolo}} {{nome_atendente}}" })).toBeNull();
    expect(replyError({ shortcode: "ola", title: "Olá", content: "Oi {{email}}" })).toMatch(/variáveis/);
    expect(replyError({ shortcode: "ola", title: "Olá", content: "Oi {nome}" })).toMatch(/avulsa/);
    expect(replyError({ shortcode: "ola", title: "Olá", content: "Oi <b>Ana</b>" })).toMatch(/não aceita/);
    expect(prefixError("{{nome_atendente}}: ")).toBeNull();
    expect(prefixError("{{nome_cliente}}: ")).toMatch(/nome_atendente/);
  });

  it("insere o texto renderizado sem prefixo e aponta repetição", () => {
    expect(slashToken("antes /ola resto", 10)).toEqual({ start: 6, query: "ola" });
    expect(insertRendered("antes /ola resto", 6, 10, "Olá Ana")).toBe("antes Olá Ana resto");
    const signature = { enabled: true, prefix: "{{nome_atendente}}: " };
    expect(repeatsServerPrefix("ana@provedor.test: Olá", signature, "ana@provedor.test")).toBe(true);
    expect(repeatsServerPrefix("Olá", signature, "ana@provedor.test")).toBe(false);
    expect(repeatsServerPrefix("ana@provedor.test: Olá", { enabled: false, prefix: "{{nome_atendente}}: " }, "ana@provedor.test")).toBe(false);
  });
});
