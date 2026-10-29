import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { clearBrowserStorage } from "@/lib/browser-storage";
import { useSessionStore } from "@/stores/session-store";

type Scope = {
  tenantId: string | null;
  userId: string | null;
};

function sameScope(left: Scope, right: Scope): boolean {
  return left.tenantId === right.tenantId && left.userId === right.userId;
}

export function SessionQuerySync() {
  const tenantId = useSessionStore((state) => state.tenantId);
  const userId = useSessionStore((state) => state.principal?.userId ?? null);
  const queryClient = useQueryClient();
  const previousScope = useRef<Scope>({ tenantId, userId });

  useEffect(() => {
    const previous = previousScope.current;
    const next = { tenantId, userId };
    if (sameScope(previous, next)) {
      return;
    }
    previousScope.current = next;
    // Empty -> principal is restoration or the first sign-in. Clearing there
    // wipes the same principal's storage and refetches the session query.
    const replacedPrincipal =
      previous.tenantId !== null &&
      next.tenantId !== null &&
      (previous.tenantId !== next.tenantId || previous.userId !== next.userId);
    if (!replacedPrincipal) {
      return;
    }
    clearBrowserStorage();
    queryClient.clear();
    if (useSessionStore.getState().displayName !== "") {
      useSessionStore.getState().setDisplayName("");
    }
  }, [queryClient, tenantId, userId]);

  return null;
}
