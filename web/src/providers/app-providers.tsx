import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { createQueryClient } from "@/lib/query-client";
import { SessionLifecycle } from "@/providers/session-lifecycle";
import { SessionQuerySync } from "@/providers/session-query-sync";

type AppProvidersProps = {
  children: ReactNode;
  client?: QueryClient;
};

export function AppProviders({ children, client }: AppProvidersProps) {
  const [owned] = useState(createQueryClient);
  const queryClient = client ?? owned;

  return (
    <QueryClientProvider client={queryClient}>
      <SessionQuerySync />
      <SessionLifecycle>{children}</SessionLifecycle>
    </QueryClientProvider>
  );
}
