import { useEffect, useRef } from "react";
import type { PreferenceValues } from "./notification-preferences";

// Adapted by the integration from server-authorized live events, never raw DTOs.
export type NotificationEvent = { id: string; cursor: number; kind: "new_conversation" | "new_message"; live: boolean };
export type NotificationBatch = { scope: string; events: readonly NotificationEvent[] };

function play(kind: NotificationEvent["kind"], active: Set<() => void>) {
  try {
    const audio = new AudioContext();
    const oscillator = audio.createOscillator();
    const gain = audio.createGain();
    oscillator.frequency.value = kind === "new_conversation" ? 880 : 520;
    gain.gain.setValueAtTime(0.06, audio.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.001, audio.currentTime + 0.15);
    oscillator.connect(gain); gain.connect(audio.destination);
    oscillator.start(); oscillator.stop(audio.currentTime + 0.15);
    const dispose = () => {
      active.delete(dispose);
      try { oscillator.onended = null; oscillator.stop(); } catch { /* already stopped */ }
      void audio.close().catch(() => {});
    };
    active.add(dispose);
    oscillator.onended = dispose;
    void audio.resume().catch(dispose);
  } catch { /* Audio can be unavailable or blocked by autoplay. */ }
}

export function useNotificationEvents(scope: string | null, prefs: PreferenceValues, batch: NotificationBatch | null, onEvent: (event: NotificationEvent) => void) {
  const state = useRef<{ scope: string | null; cursor: number; seen: Set<string> }>({ scope: null, cursor: 0, seen: new Set() });
  const active = useRef(new Set<() => void>());
  const callbacks = useRef({ prefs, onEvent }); callbacks.current = { prefs, onEvent };
  useEffect(() => {
    const current = state.current;
    if (current.scope !== scope) { current.scope = scope; current.cursor = 0; current.seen.clear(); for (const d of [...active.current]) d(); }
    if (!scope || !batch || batch.scope !== scope) return;
    for (const event of batch.events) {
      if (!event.id || !Number.isSafeInteger(event.cursor) || event.cursor <= 0 || current.seen.has(event.id) || event.cursor <= current.cursor) continue;
      current.cursor = event.cursor; current.seen.add(event.id);
      if (!event.live) continue;
      const { prefs: settings, onEvent: notify } = callbacks.current;
      notify(event);
      if (settings.silent) continue;
      if (settings.sound) play(event.kind, active.current);
      if (settings.browser && typeof Notification !== "undefined" && Notification.permission === "granted") {
        try { new Notification(event.kind === "new_conversation" ? "Nova conversa" : "Nova mensagem"); } catch { /* Browser notifications can fail independently. */ }
      }
    }
  }, [scope, batch]);
  // Silent mode or sound off discards pending/active audio so a late autoplay release cannot play it.
  const muted = prefs.silent || !prefs.sound;
  useEffect(() => { if (muted) for (const d of [...active.current]) d(); }, [muted]);
  // Dedup state survives StrictMode effect replays; audio is released on real unmount (deferred so a replay can cancel it).
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (timer.current) { clearTimeout(timer.current); timer.current = null; }
    return () => { timer.current = setTimeout(() => { timer.current = null; for (const d of [...active.current]) d(); state.current.scope = null; }, 0); };
  }, []);
}
