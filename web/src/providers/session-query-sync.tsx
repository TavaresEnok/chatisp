import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useSessionStore } from "@/stores/session-store";

export function SessionQuerySync() {
  const tenantId = useSessionStore((state) => state.tenantId);
  const queryClient = useQueryClient();
  const previousTenantId = useRef(tenantId);

  useEffect(() => {
    if (previousTenantId.current === tenantId) {
      return;
    }
    queryClient.clear();
    previousTenantId.current = tenantId;
  }, [queryClient, tenantId]);

  return null;
}
