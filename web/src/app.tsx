import { BrowserRouter } from "react-router";
import { AppProviders } from "@/providers/app-providers";
import { AppRoutes } from "@/routes";

export function App() {
  return (
    <AppProviders>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </AppProviders>
  );
}
