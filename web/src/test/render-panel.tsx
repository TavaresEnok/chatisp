import { QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { AppRoutes } from "@/routes";
import { createQueryClient } from "@/lib/query-client";
import { SessionQuerySync } from "@/providers/session-query-sync";

export function renderPanel(path: string) {
  const queryClient = createQueryClient();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <SessionQuerySync />
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
      </MemoryRouter>
    </QueryClientProvider>,
  );

  return { queryClient, ...view };
}
