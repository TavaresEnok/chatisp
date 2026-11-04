import { useEffect, useState } from "react";

export type BotHookNotice = {
  id: string;
  conversation_id: string;
  state: string;
  notice: string;
  granted_epoch?: number;
};

const uncertainText = "Resultado do gancho incerto.";

export function isUncertainNotice(item: BotHookNotice) {
  return item.state === "uncertain";
}

/** Pending automation, with the uncertain result kept visible. */
export function BotHookNoticeList({ notices }: { notices: BotHookNotice[] }) {
  if (notices.length === 0) {
    return <p>Nenhuma pendência do gancho de bot.</p>;
  }
  return (
    <section aria-label="Pendências do gancho de bot">
      <h3>Pendências do gancho de bot</h3>
      <ul>
        {notices.map((item) => {
          const uncertain = isUncertainNotice(item);
          const text = item.notice || (uncertain ? uncertainText : "Pendência da automação.");
          return (
            <li key={item.id} data-state={item.state}>
              {uncertain ? <p role="alert">{text}</p> : <p role="status">{text}</p>}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** Loads the hook notices for one inbox. A missing route is an empty list, not a hidden failure. */
export function BotHookNoticePanel({ inbox }: { inbox: string }) {
  const [notices, setNotices] = useState<BotHookNotice[] | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    fetch(`/api/panel/bot-hook/notices?inbox_id=${encodeURIComponent(inbox)}`, {
      signal: controller.signal,
      credentials: "same-origin",
    })
      .then(async (response) => {
        if (response.status === 404) return { notices: [] as BotHookNotice[] };
        if (!response.ok) throw new Error("falha");
        return response.json() as Promise<{ notices?: BotHookNotice[] }>;
      })
      .then((body) => {
        setFailed(false);
        setNotices(body.notices ?? []);
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, [inbox]);
  if (failed) return <p role="alert">Não foi possível consultar as pendências do gancho.</p>;
  if (!notices) return <p>Consultando pendências do gancho.</p>;
  return <BotHookNoticeList notices={notices} />;
}
