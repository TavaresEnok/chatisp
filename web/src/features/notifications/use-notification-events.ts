import { useEffect, useEffectEvent, useRef } from "react";
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
  type Session = { scope: string | null; cursor: number; seen: Set<string>; active: Set<() => void>; timer: ReturnType<typeof setTimeout> | null };
  const session = useRef<Session | null>(null);
  const handleEvent = useEffectEvent((event: NotificationEvent, active: Set<() => void>) => {
    onEvent(event);
    if (prefs.silent) return;
    if (prefs.sound) play(event.kind, active);
    if (prefs.browser && typeof Notification !== "undefined" && Notification.permission === "granted") {
      try { new Notification(event.kind === "new_conversation" ? "Nova conversa" : "Nova mensagem"); } catch { /* Browser notifications can fail independently. */ }
    }
  });
  useEffect(() => {
    let current = session.current;
    if (!current || current.scope !== scope) {
      if (current) {
        if (current.timer) clearTimeout(current.timer);
        for (const dispose of [...current.active]) dispose();
      }
      current = { scope, cursor: 0, seen: new Set(), active: new Set(), timer: null };
      session.current = current;
    }
    if (current.timer) { clearTimeout(current.timer); current.timer = null; }
    const owned = current;
    return () => {
      owned.timer = setTimeout(() => {
        owned.timer = null;
        for (const dispose of [...owned.active]) dispose();
        if (session.current === owned) session.current = null;
      }, 0);
    };
  }, [scope]);
  useEffect(() => {
    const current = session.current;
    if (!current || !scope || !batch || batch.scope !== scope) return;
    for (const event of batch.events) {
      if (!event.id || !Number.isSafeInteger(event.cursor) || event.cursor <= 0 || current.seen.has(event.id) || event.cursor <= current.cursor) continue;
      current.cursor = event.cursor; current.seen.add(event.id);
      if (!event.live) continue;
      handleEvent(event, current.active);
    }
  }, [scope, batch]);
  // Silent mode or sound off discards pending/active audio so a late autoplay release cannot play it.
  const muted = prefs.silent || !prefs.sound;
  useEffect(() => { if (muted && session.current) for (const d of [...session.current.active]) d(); }, [muted, scope]);
}
