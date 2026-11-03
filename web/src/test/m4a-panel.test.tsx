import { describe, expect, it } from "vitest";
import { previewKind } from "@/features/attachments/media";
import { windowIndicator } from "@/features/channelpolicy/policy";
import { insertRendered } from "@/features/cannedreplies/rules";

describe("limites do painel M4A", () => {
  it("não calcula janela, não executa HTML e não prefixa o rascunho", () => {
    expect(windowIndicator("meta_cloud")).toBe("unavailable");
    expect(windowIndicator("evolution")).toBe("hidden");
    expect(previewKind("text/html")).toBe("refused");
    expect(previewKind("image/svg+xml")).toBe("refused");
    expect(insertRendered("/ola", 0, 4, "Texto pronto")).toBe("Texto pronto");
  });
});
