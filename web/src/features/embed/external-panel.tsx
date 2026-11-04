import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { getConversationEmbed, type EmbedView } from "@/api/embed";
import { acceptFrameMessage, canEmbed, publish } from "@/features/embed/rules";
import type { PanelPrincipal } from "@/stores/session-store";

export function ExternalPanel({ principal, inboxId, conversationId }: { principal: PanelPrincipal; inboxId: string; conversationId: string }) {
  const query = useQuery({
    queryKey: ["m6-embed", principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId, inboxId, conversationId],
    queryFn: ({ signal }) => getConversationEmbed(inboxId, conversationId, signal),
    retry: false,
  });
  if (!query.data || !canEmbed(query.data)) return null;
  return <PanelFrame view={query.data} />;
}

function PanelFrame({ view }: { view: EmbedView }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const origin = view.origin ?? "";
  const context = view.context;
  useEffect(() => {
    function onMessage(event: MessageEvent) {
      if (!acceptFrameMessage(event, origin, frame.current?.contentWindow ?? null)) return;
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [origin]);
  if (!context || !view.url) return null;
  return (
    <section className="painel-externo flex min-w-0 flex-col gap-2 lg:col-start-3 lg:row-start-1" aria-label="Painel externo">
      <h2 className="text-sm font-medium">Painel externo</h2>
      <iframe
        title="Painel externo"
        src={view.url}
        sandbox="allow-scripts allow-forms"
        referrerPolicy="no-referrer"
        className="min-h-96 w-full rounded-xl bg-card ring-1 ring-foreground/10"
        ref={frame}
        onLoad={(event) => publish(event.currentTarget.contentWindow, context, origin)}
      />
      <a href={view.url} target="_blank" rel="noopener noreferrer">Abrir fora do chat</a>
    </section>
  );
}
