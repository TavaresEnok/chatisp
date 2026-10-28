import { api } from "@/api/client";
import type { AreaId } from "@/lib/areas";

export type AreaItem = {
  id: string;
  label: string;
};

export type AreaSnapshot = {
  tenantId: string;
  area: AreaId;
  items: readonly AreaItem[];
};

export async function fetchAreaSnapshot(
  area: AreaId,
  tenantId: string,
): Promise<AreaSnapshot> {
  // The generated client has no panel operation until the backend publishes OpenAPI.
  // The client stays wired here so a later route replaces this empty snapshot.
  void api;

  return {
    tenantId,
    area,
    items: [],
  };
}
