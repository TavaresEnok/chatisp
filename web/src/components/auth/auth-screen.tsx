import { MessagesSquare } from "lucide-react";
import type { ReactNode } from "react";

type AuthScreenProps = {
  title: string;
  children: ReactNode;
};

export function AuthScreen({ title, children }: AuthScreenProps) {
  return (
    <main
      id="conteudo"
      tabIndex={-1}
      className="auth-screen"
    >
      <header className="flex flex-col gap-2">
        <span className="auth-symbol"><MessagesSquare size={30} aria-hidden="true" /></span>
        <p className="text-sm font-medium text-muted-foreground">CHAT ISP · ATENDIMENTO DO PROVEDOR</p>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      </header>
      {children}
    </main>
  );
}
