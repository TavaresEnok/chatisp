import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { createQueryClient } from "@/lib/query-client";
import { AppProviders } from "@/providers/app-providers";
import { SessionLossRedirect } from "@/providers/session-loss-redirect";
import { AppRoutes } from "@/routes";

export function renderAuth(path: string) {
  const queryClient = createQueryClient();
  const view = render(
    <AppProviders client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <SessionLossRedirect />
        <AppRoutes />
      </MemoryRouter>
    </AppProviders>,
  );
  return { queryClient, ...view };
}
