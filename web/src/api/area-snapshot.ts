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
  // /healthz and /readyz only describe the process. They are not a queue,
  // a directory, or a report, so this snapshot stays empty until a collection
  // route exists. The client stays referenced for that route.
  void api;

  return {
    tenantId,
    area,
    items: [],
  };
}
