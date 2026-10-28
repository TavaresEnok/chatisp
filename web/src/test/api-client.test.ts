import { describe, expect, it } from "vitest";
import { api } from "@/api/client";

describe("cliente HTTP", () => {
  it("chama a rota publicada na mesma origem, com cookie de sessão", async () => {
    const seen: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      if (!(input instanceof Request)) {
        throw new Error("pedido sem Request");
      }
      seen.push(input);
      return new Response(JSON.stringify({ status: "ok" }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    };

    const result = await api.GET("/healthz", { fetch: fetchMock });

    expect(result.data?.status).toBe("ok");
    expect(result.error).toBeUndefined();
    expect(seen).toHaveLength(1);
    const request = seen[0];
    expect(request).toBeDefined();
    expect(new URL(request?.url ?? "").pathname).toBe("/healthz");
    expect(request?.credentials).toBe("include");
  });
});
