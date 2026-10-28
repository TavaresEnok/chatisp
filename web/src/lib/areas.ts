export const areas = [
  {
    id: "dashboard",
    path: "/painel",
    label: "Painel",
    emptyTitle: "Nada para acompanhar",
    signedOut:
      "Nenhum provedor em sessão. Fila, espera, presença e volume aparecem aqui apenas quando houver atendimento real.",
    empty:
      "Não há fila, espera nem presença registradas para este provedor.",
  },
  {
    id: "conversations",
    path: "/conversas",
    label: "Conversas",
    emptyTitle: "Nenhuma conversa",
    signedOut:
      "Nenhum provedor em sessão. A lista mostra somente conversas reais depois do acesso.",
    empty: "Nenhuma conversa registrada para este provedor.",
  },
  {
    id: "contacts",
    path: "/contatos",
    label: "Contatos",
    emptyTitle: "Nenhum contato",
    signedOut:
      "Nenhum provedor em sessão. O diretório mostra pessoas das conversas reais depois do acesso.",
    empty: "Nenhum contato registrado para este provedor.",
  },
  {
    id: "teams",
    path: "/equipes",
    label: "Equipes",
    emptyTitle: "Nenhuma equipe",
    signedOut:
      "Nenhum provedor em sessão. Setores, membros e capacidade aparecem depois do cadastro no servidor.",
    empty: "Nenhuma equipe cadastrada para este provedor.",
  },
  {
    id: "reports",
    path: "/relatorios",
    label: "Relatórios",
    emptyTitle: "Nenhum relatório",
    signedOut:
      "Nenhum provedor em sessão. Períodos, tempos e exportação usam atendimentos já registrados, sem números de exemplo.",
    empty:
      "Ainda não há atendimento registrado para calcular períodos, tempos ou exportação.",
  },
  {
    id: "settings",
    path: "/configuracoes",
    label: "Configurações",
    emptyTitle: "Nada configurado no servidor",
    signedOut:
      "Nenhum provedor em sessão. Usuários, números, horários, respostas prontas, etiquetas e integrações ficam nesta área.",
    empty:
      "Nenhum usuário, número, horário, resposta pronta, etiqueta ou integração registrada para este provedor.",
  },
] as const;

export type AreaId = (typeof areas)[number]["id"];
export type AreaDefinition = (typeof areas)[number];

export function findArea(areaId: AreaId): AreaDefinition {
  const area = areas.find((item) => item.id === areaId);
  if (!area) {
    throw new Error("Área desconhecida.");
  }
  return area;
}
