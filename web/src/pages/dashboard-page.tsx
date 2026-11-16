import "@/features/dashboard/dashboard-design.css";
import { useQuery } from "@tanstack/react-query";
import { listPackages } from "@/api/packages";
import type { PanelPrincipal } from "@/stores/session-store";
import { AreaPage } from "@/components/layout/area-page";
import { AttendanceReportView } from "@/features/reports/attendance-report";
import { useSessionStore } from "@/stores/session-store";

export function DashboardPage() {
  const principal = useSessionStore((state) => state.principal);
  if (!principal || principal.mfaEnrollmentRequired) return <AreaPage areaId="dashboard" />;
  return <article className="dashboard-page">
    <header className="dashboard-header"><div><p className="dashboard-eyebrow">Operação</p><h1>Painel</h1><p className="dashboard-description">Acompanhe a situação atual do atendimento.</p></div></header>
    <AttendanceReportView principal={principal} mode="live" />
    {principal.role === "admin" && <DownloadPackages principal={principal} />}
  </article>;
}

function DownloadPackages({ principal }: { principal: PanelPrincipal }) {
  const query = useQuery({
    queryKey: ["download-packages", principal.tenantId, principal.sessionId, principal.userId, principal.membershipId, principal.role],
    queryFn: ({ signal }) => listPackages(signal),
    retry: false,
  });
  return (
    <details className="dashboard-packages">
      <summary><span><strong>Pacotes para download</strong><small>Arquivos disponíveis para administradores</small></span></summary>
      <div className="dashboard-packages-content">
      {query.isPending && <p role="status" className="mt-2 text-sm text-muted-foreground">Carregando pacotes…</p>}
      {query.isError && <div role="alert" className="mt-2 text-sm">
        <p>Não foi possível carregar os pacotes.</p>
        <button type="button" className="mt-2 underline" onClick={() => void query.refetch()}>Tentar novamente</button>
      </div>}
      {query.isSuccess && (query.data.length === 0 ? <p className="mt-2 text-sm text-muted-foreground">Nenhum pacote disponível no momento.</p> :
        <ul className="mt-3 space-y-4">{query.data.map((item) => <li key={item.id} className="text-sm">
          <h3 className="font-medium">{item.title}</h3>
          <p className="text-muted-foreground">{item.description}</p>
          <p className="text-muted-foreground">{item.filename} · {formatSize(item.size)}</p>
          {item.sha256 && <details className="mt-1"><summary className="cursor-pointer">SHA-256</summary><code className="break-all">{item.sha256}</code></details>}
          <a className="mt-2 inline-block underline" href={`/api/panel/packages/${encodeURIComponent(item.id)}/download`} download>Baixar {item.title}</a>
        </li>)}</ul>)}
      </div>
    </details>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
