import { AreaPage } from "@/components/layout/area-page";
import { AttendanceReportView } from "@/features/reports/attendance-report";
import { useSessionStore } from "@/stores/session-store";

export function DashboardPage() {
  const principal = useSessionStore((state) => state.principal);
  if (!principal || principal.mfaEnrollmentRequired) return <AreaPage areaId="dashboard" />;
  return <article className="dashboard-page">
    <h1 className="text-2xl font-semibold tracking-tight">Painel</h1>
    <AttendanceReportView principal={principal} mode="live" />
  </article>;
}
