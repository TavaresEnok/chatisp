import type { ReactNode } from "react";
import { Link } from "react-router";

type AuthScreenProps = {
  title: string;
  children: ReactNode;
};

export function AuthScreen({ title, children }: AuthScreenProps) {
  return (
    <main
      id="conteudo"
      tabIndex={-1}
      className="mx-auto flex min-h-svh max-w-md flex-col gap-6 px-4 py-10"
    >
      <header className="flex flex-col gap-2">
        <p className="text-sm font-medium text-muted-foreground">Chat ISP</p>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      </header>
      {children}
      <p>
        <Link
          to="/painel"
          className="text-sm font-medium text-foreground underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
        >
          Ir para o Painel
        </Link>
      </p>
    </main>
  );
}
