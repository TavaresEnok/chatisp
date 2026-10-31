import { useEffect, useRef, useState } from "react";
import { heartbeat, indicators, release } from "@/api/collision";
import type { components } from "@/api/schema";

export function useCollision(inbox: string | null, conversation: string | null) {
  const [people, setPeople] = useState<components["schemas"]["Indicator"][]>([]);
  const typed = useRef(0);
  const trigger = useRef<() => void>(() => {});
  useEffect(() => {
    setPeople([]);
    if (!inbox || !conversation) return;
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
        if (active) setPeople(result.indicators ?? []);
      } catch { if (active) setPeople([]); }
      finally { inflight = false; }
    }
    trigger.current = () => { const delay = Math.max(0, 3000 - (Date.now() - lastSent)); if (typingTimer) clearTimeout(typingTimer); typingTimer = setTimeout(() => void tick(), delay); };
    function visibility() {
      if (document.visibilityState === "visible" && document.hasFocus()) void tick();
      else { setPeople([]); if (registered && !inflight) { registered = false; void release(inbox!, conversation!, tab).catch(() => {}); } }
    }
    void tick();
    const interval = setInterval(() => void tick(), 10000);
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("focus", visibility); window.addEventListener("blur", visibility);
    return () => { active = false; trigger.current = () => {}; clearInterval(interval); if (typingTimer) clearTimeout(typingTimer); document.removeEventListener("visibilitychange", visibility); window.removeEventListener("focus", visibility); window.removeEventListener("blur", visibility); if (registered && !inflight) void release(inbox, conversation, tab).catch(() => {}); };
  }, [inbox, conversation]);
  return { people, typing: () => { typed.current = Date.now(); trigger.current(); } };
}
