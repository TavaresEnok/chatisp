import { useEffect, useRef, useState } from "react";
import { heartbeat, indicators, release } from "@/api/collision";
import type { components } from "@/api/schema";

export function useCollision(inbox: string | null, conversation: string | null) {
  const scopeKey = `${inbox ?? "\u0000"}\0${conversation ?? "\u0000"}`;
  const [activation, setActivation] = useState({ scope: scopeKey, visit: 0 });
  if (activation.scope !== scopeKey) setActivation({ scope: scopeKey, visit: activation.visit + 1 });
  const visit = activation.scope === scopeKey ? activation.visit : activation.visit + 1;
  const [fetched, setFetched] = useState<{ visit: number; people: components["schemas"]["Indicator"][] }>({ visit: -1, people: [] });
  const people = fetched.visit === visit ? fetched.people : [];
  const typed = useRef(0);
  const trigger = useRef<() => void>(() => {});
  useEffect(() => {
    if (!inbox || !conversation) return;
    const currentVisit = visit;
    const tab = crypto.randomUUID();
    let active = true, inflight = false, registered = false, lastSent = 0;
    let typingTimer: ReturnType<typeof setTimeout> | undefined;
    async function tick() {
      if (!active || inflight || document.visibilityState !== "visible" || !document.hasFocus()) return;
      inflight = true;
      lastSent = Date.now();
      try {
        await heartbeat(inbox!, conversation!, tab, Date.now() - typed.current < 5000);
        registered = true;
        if (!active || document.visibilityState !== "visible" || !document.hasFocus()) { registered = false; await release(inbox!, conversation!, tab); return; }
        const result = await indicators(inbox!, conversation!);
        if (active) setFetched({ visit: currentVisit, people: result.indicators ?? [] });
      } catch { if (active) setFetched({ visit: currentVisit, people: [] }); }
      finally { inflight = false; }
    }
    trigger.current = () => { const delay = Math.max(0, 3000 - (Date.now() - lastSent)); if (typingTimer) clearTimeout(typingTimer); typingTimer = setTimeout(() => void tick(), delay); };
    function visibility() {
      if (document.visibilityState === "visible" && document.hasFocus()) void tick();
      else { setFetched({ visit: currentVisit, people: [] }); if (registered && !inflight) { registered = false; void release(inbox!, conversation!, tab).catch(() => {}); } }
    }
    void tick();
    const interval = setInterval(() => void tick(), 10000);
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("focus", visibility); window.addEventListener("blur", visibility);
    return () => { active = false; trigger.current = () => {}; clearInterval(interval); if (typingTimer) clearTimeout(typingTimer); document.removeEventListener("visibilitychange", visibility); window.removeEventListener("focus", visibility); window.removeEventListener("blur", visibility); if (registered && !inflight) void release(inbox, conversation, tab).catch(() => {}); };
  }, [inbox, conversation, scopeKey, visit]);
  return { people, typing: () => { typed.current = Date.now(); trigger.current(); } };
}
