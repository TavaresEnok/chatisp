import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "@/components/layout/app-shell";
import { AreaPage } from "@/components/layout/area-page";
import { SettingsPage } from "@/pages/settings-page";
import { UnknownPage } from "@/pages/unknown-page";

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/painel" replace />} />
        <Route path="painel" element={<AreaPage areaId="dashboard" />} />
        <Route path="conversas" element={<AreaPage areaId="conversations" />} />
        <Route path="contatos" element={<AreaPage areaId="contacts" />} />
        <Route path="equipes" element={<AreaPage areaId="teams" />} />
        <Route path="relatorios" element={<AreaPage areaId="reports" />} />
        <Route path="configuracoes" element={<SettingsPage />} />
        <Route path="*" element={<UnknownPage />} />
      </Route>
    </Routes>
  );
}
