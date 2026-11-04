import type { ReactNode } from "react";
import { ErpPanel } from "@/features/erp/erp-panel";
import type { PanelPrincipal } from "@/stores/session-store";

type Props = {
  principal: PanelPrincipal | null;
  conversationId: string;
  protocol: string;
  children?: ReactNode;
};

export function ConversationSidebar({ principal, conversationId, protocol, children }: Props) {
  return (
    <div className="painel-lateral flex min-w-0 flex-col gap-4 lg:col-start-3 lg:row-start-1">
      {principal ? <ErpPanel key={conversationId} principal={principal} conversationId={conversationId} localProtocol={protocol} /> : null}
      {children}
    </div>
  );
}
