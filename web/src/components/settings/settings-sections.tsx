import { Link } from "react-router";
import { useSessionStore } from "@/stores/session-store";

const sections = [
  { title: "Usuários", description: "Pessoas, papéis e convites do provedor.", to: "/configuracoes/usuarios", roles: ["admin"] },
  { title: "Caixas e números", description: "Configuração dos canais e estado das conexões.", to: "/configuracoes/canais", roles: ["admin"] },
  { title: "Equipes e setores", description: "Organização e vínculos dos atendentes.", to: "/equipes", roles: ["admin", "supervisor", "agent"] },
  { title: "Horários", description: "Expediente e regras de plantão.", to: "#config-horarios", roles: ["admin", "supervisor"] },
  { title: "Respostas prontas", description: "Textos compartilhados e atalhos do compositor.", to: "#config-respostas", roles: ["admin", "supervisor", "agent"] },
  { title: "Etiquetas e notas", description: "Gerencie no contexto de cada atendimento.", to: "/conversas", roles: ["admin", "supervisor", "agent"] },
  { title: "Distribuição", description: "Ocupação e atribuição dos atendimentos.", to: "#config-distribuicao", roles: ["admin", "supervisor"] },
  { title: "Automação", description: "Adiamento, triagem e regras de inatividade.", to: "#config-automacao", roles: ["admin", "supervisor"] },
  { title: "Integrações", description: "API, webhooks e estado dos conectores.", to: "#config-integracoes", roles: ["admin"] },
] as const;

export function SettingsSections() {
  const role = useSessionStore(s => s.principal?.role);
  return <nav aria-label="Partes das configurações" className="settings-shortcuts">
    {sections.filter(section => role && (section.roles as readonly string[]).includes(role)).map(section => {
      const content = <><strong>{section.title}</strong><span>{section.description}</span></>;
      return section.to.startsWith("#") ? <a key={section.title} href={section.to}>{content}</a> : <Link key={section.title} to={section.to}>{content}</Link>;
    })}
  </nav>;
}
