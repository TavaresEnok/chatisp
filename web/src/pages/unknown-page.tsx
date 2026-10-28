import { Link } from "react-router";

export function UnknownPage() {
  return (
    <article className="flex max-w-3xl flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight">
        Página não encontrada
      </h1>
      <p className="text-sm text-muted-foreground">
        Este endereço não faz parte do painel.
      </p>
      <Link
        to="/painel"
        className="w-fit rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
      >
        Ir para o Painel
      </Link>
    </article>
  );
}
