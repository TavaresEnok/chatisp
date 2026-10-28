import { describe, expect, it } from "vitest";
import { areas } from "@/lib/areas";
import { areaQueryKey, queryKeys } from "@/lib/query-keys";

describe("chaves de consulta", () => {
  it("inclui o provedor em todas as áreas", () => {
    for (const area of areas) {
      const key = areaQueryKey(area.id, "provedor-a");
      expect(key[0]).toBe("provedor-a");
      expect(key).not.toEqual(areaQueryKey(area.id, "provedor-b"));
    }
  });

  it("separa conversa e contato dentro do provedor", () => {
    expect(queryKeys.conversation("provedor-a", "c1")).toEqual([
      "provedor-a",
      "conversations",
      "c1",
    ]);
    expect(queryKeys.contact("provedor-a", "p1")).toEqual([
      "provedor-a",
      "contacts",
      "p1",
    ]);
    expect(queryKeys.tenant("provedor-a")).toEqual(["provedor-a"]);
  });
});
