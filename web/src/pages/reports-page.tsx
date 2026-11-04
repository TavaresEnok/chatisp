import { AreaPage } from "@/components/layout/area-page";
import { AttendanceReportView } from "@/features/reports/attendance-report";
import { useSessionStore } from "@/stores/session-store";

export function ReportsPage() {
  const principal = useSessionStore((state) => state.principal);
  if (!principal || principal.mfaEnrollmentRequired) return <AreaPage areaId="reports" />;
  return <article className="flex max-w-3xl flex-col gap-6">
    <h1 className="text-2xl font-semibold tracking-tight">Relatórios</h1>
    <AttendanceReportView principal={principal} mode="full" />
  </article>;
}
