import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { fetchAreaSnapshot } from "@/api/area-snapshot";
import { AreaStatus } from "@/components/layout/area-status";
import { VirtualItemList } from "@/components/layout/virtual-item-list";
import { findArea, type AreaId } from "@/lib/areas";
import { areaQueryKey, signedOutQueryKey } from "@/lib/query-keys";
import { useSessionStore } from "@/stores/session-store";

type AreaPageProps = {
  areaId: AreaId;
  children?: ReactNode;
};

export function AreaPage({ areaId, children }: AreaPageProps) {
  const area = findArea(areaId);
  const tenantId = useSessionStore((state) => state.tenantId);
  const query = useQuery({
    queryKey:
      tenantId === null
        ? signedOutQueryKey(area.id)
        : areaQueryKey(area.id, tenantId),
    enabled: tenantId !== null,
    queryFn: () => {
      if (tenantId === null) {
        return Promise.reject(new Error("Área sem provedor."));
      }
      return fetchAreaSnapshot(area.id, tenantId);
    },
  });

  const description = tenantId === null ? area.signedOut : area.empty;
  const snapshot = query.data;

  return (
    <article className="flex max-w-3xl flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">{area.label}</h1>
      {tenantId === null ? (
        <AreaStatus
          status="signed-out"
          title={area.emptyTitle}
          description={description}
        />
      ) : null}
      {tenantId !== null && query.isError ? (
        <AreaStatus
          status="error"
          title={area.emptyTitle}
          description={description}
        />
      ) : null}
      {tenantId !== null && !query.isError && query.isPending ? (
        <AreaStatus
          status="loading"
          title={area.emptyTitle}
          description={description}
        />
      ) : null}
      {tenantId !== null &&
      !query.isError &&
      !query.isPending &&
      (snapshot?.items.length ?? 0) === 0 ? (
        <AreaStatus
          status="empty"
          title={area.emptyTitle}
          description={description}
        />
      ) : null}
      {snapshot && snapshot.items.length > 0 ? (
        <VirtualItemList items={snapshot.items} />
      ) : null}
      {children}
    </article>
  );
}
