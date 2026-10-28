const sections = [
  {
    title: "Usuários",
    description:
      "Ainda sem registros. O convite de pessoas depende do servidor.",
  },
  {
    title: "Números",
    description:
      "Ainda sem registros. Cada número entra quando o canal estiver ligado.",
  },
  {
    title: "Horários",
    description:
      "Ainda sem registros. O expediente do provedor depende do servidor.",
  },
  {
    title: "Respostas prontas",
    description:
      "Ainda sem registros. Os textos compartilhados dependem do servidor.",
  },
  {
    title: "Etiquetas",
    description:
      "Ainda sem registros. Os motivos do contato dependem do servidor.",
  },
  {
    title: "Integrações",
    description:
      "Ainda sem registros. Nada aqui representa uma conexão real.",
  },
] as const;

export function SettingsSections() {
  return (
    <section aria-labelledby="partes-das-configuracoes" className="flex flex-col gap-3">
      <h2 id="partes-das-configuracoes" className="text-base font-medium">
        Partes desta área
      </h2>
      <ul className="grid gap-3 sm:grid-cols-2" aria-label="Partes das configurações">
        {sections.map((section) => (
          <li
            key={section.title}
            className="rounded-xl bg-card p-4 text-card-foreground ring-1 ring-foreground/10"
          >
            <h3 className="text-sm font-medium">{section.title}</h3>
            <p className="mt-1 text-sm text-muted-foreground">
              {section.description}
            </p>
          </li>
        ))}
      </ul>
    </section>
  );
}
