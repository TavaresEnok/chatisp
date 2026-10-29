import { BrowserRouter } from "react-router";
import { AppProviders } from "@/providers/app-providers";
import { SessionLossRedirect } from "@/providers/session-loss-redirect";
import { AppRoutes } from "@/routes";

export function App() {
  return (
    <AppProviders>
      <BrowserRouter>
        <SessionLossRedirect />
        <AppRoutes />
      </BrowserRouter>
    </AppProviders>
  );
}
