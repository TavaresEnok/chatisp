import { StrictMode } from "react";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { NotificationPreferences } from "./notification-preferences";
import { useNotificationEvents, type NotificationBatch } from "./use-notification-events";

const prefs = { sound: false, browser: true, silent: false };
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it("requests browser permission only on explicit action and reports denial", async () => {
  const requestPermission = vi.fn().mockResolvedValue("denied");
  vi.stubGlobal("Notification", { permission: "default", requestPermission });
  render(<NotificationPreferences prefs={prefs} onSave={async () => {}} />);
  expect(requestPermission).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Permitir notificações" }));
  await waitFor(() => expect(screen.getByText("Permissão recusada no navegador.")).toBeTruthy());
  expect(requestPermission).toHaveBeenCalledOnce();
});
it("reports unavailable and save failures", async () => {
  vi.stubGlobal("Notification", undefined);
  render(<NotificationPreferences prefs={prefs} onSave={async () => { throw new Error(); }} />);
  expect(screen.getByText("Notificações indisponíveis neste navegador.")).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Som"));
  await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
});
it("deduplicates live events, suppresses replay and silent signals, and rejects old scope", () => {
  const browser = vi.fn(); Object.assign(browser, { permission: "granted" }); vi.stubGlobal("Notification", browser);
  const callback = vi.fn();
  function Harness({ scope, batch, silent = false }: { scope: string | null; batch: NotificationBatch | null; silent?: boolean }) { useNotificationEvents(scope, { ...prefs, silent }, batch, callback); return null; }
  const batch: NotificationBatch = { scope: "a", events: [{ id: "old", cursor: 1, kind: "new_message", live: false }, { id: "new", cursor: 2, kind: "new_message", live: true }] };
  const view = render(<Harness scope="a" batch={batch} />);
  expect(callback).toHaveBeenCalledTimes(1); expect(browser).toHaveBeenCalledTimes(1);
  view.rerender(<Harness scope="a" batch={{ ...batch }} />); expect(callback).toHaveBeenCalledTimes(1);
  view.rerender(<Harness scope="b" batch={batch} />); expect(callback).toHaveBeenCalledTimes(1);
  view.rerender(<Harness scope="b" silent batch={{ scope: "b", events: [{ id: "b", cursor: 1, kind: "new_conversation", live: true }] }} />);
  expect(callback).toHaveBeenCalledTimes(2); expect(browser).toHaveBeenCalledTimes(1);
  view.rerender(<Harness scope={null} batch={batch} />); view.unmount();
  expect(callback).toHaveBeenCalledTimes(2);
});
it("uses distinct tones and tolerates blocked audio", () => {
  const frequencies: Array<{ value: number }> = [];
  class FakeAudio {
    currentTime = 0; destination = {};
    createOscillator() { const frequency = { value: 0 }; frequencies.push(frequency); return { frequency, connect: vi.fn(), start: vi.fn(), stop: vi.fn(), onended: null }; }
    createGain() { return { gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() }, connect: vi.fn() }; }
    resume() { return Promise.reject(new Error("blocked")); }
    close() { return Promise.resolve(); }
  }
  vi.stubGlobal("AudioContext", FakeAudio);
  function Harness() { useNotificationEvents("a", { sound: true, browser: false, silent: false }, { scope: "a", events: [{ id: "1", cursor: 1, kind: "new_conversation", live: true }, { id: "2", cursor: 2, kind: "new_message", live: true }] }, () => {}); return null; }
  render(<Harness />);
  expect(frequencies.map(f => f.value)).toEqual([880, 520]);
});
it("keeps dedup under StrictMode and closes pending audio on scope change and unmount", async () => {
  const closes = vi.fn();
  class PendingAudio {
    currentTime = 0; destination = {};
    createOscillator() { return { frequency: { value: 0 }, connect: vi.fn(), start: vi.fn(), stop: vi.fn(), onended: null }; }
    createGain() { return { gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() }, connect: vi.fn() }; }
    resume() { return new Promise<void>(() => {}); }
    close() { closes(); return Promise.resolve(); }
  }
  vi.stubGlobal("AudioContext", PendingAudio);
  const callback = vi.fn();
  const batch: NotificationBatch = { scope: "a", events: [{ id: "1", cursor: 1, kind: "new_message", live: true }] };
  function Harness({ scope, b }: { scope: string; b: NotificationBatch }) { useNotificationEvents(scope, { sound: true, browser: false, silent: false }, b, callback); return null; }
  const view = render(<StrictMode><Harness scope="a" b={batch} /></StrictMode>);
  expect(callback).toHaveBeenCalledTimes(1);
  await new Promise(r => setTimeout(r, 5));
  expect(closes).not.toHaveBeenCalled();
  view.rerender(<StrictMode><Harness scope="b" b={{ scope: "b", events: [] }} /></StrictMode>);
  expect(closes).toHaveBeenCalledTimes(1);
  view.rerender(<StrictMode><Harness scope="b" b={{ scope: "b", events: [{ id: "2", cursor: 1, kind: "new_message", live: true }] }} /></StrictMode>);
  view.unmount();
  await new Promise(r => setTimeout(r, 5));
  expect(closes).toHaveBeenCalledTimes(2);
});
it("discards pending audio when silent turns on or sound turns off, so late autoplay release stays quiet", async () => {
  const closes = vi.fn(); const starts = vi.fn(); let release: () => void = () => {};
  class PendingAudio {
    currentTime = 0; destination = {};
    createOscillator() { return { frequency: { value: 0 }, connect: vi.fn(), start: starts, stop: vi.fn(), onended: null }; }
    createGain() { return { gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() }, connect: vi.fn() }; }
    resume() { return new Promise<void>(r => { release = r; }); }
    close() { closes(); return Promise.resolve(); }
  }
  vi.stubGlobal("AudioContext", PendingAudio);
  const batch: NotificationBatch = { scope: "a", events: [{ id: "1", cursor: 1, kind: "new_message", live: true }] };
  function Harness({ silent, sound = true }: { silent: boolean; sound?: boolean }) { useNotificationEvents("a", { sound, browser: false, silent }, batch, () => {}); return null; }
  const view = render(<Harness silent={false} />);
  expect(closes).not.toHaveBeenCalled();
  view.rerender(<Harness silent />);
  expect(closes).toHaveBeenCalledTimes(1);
  release(); await new Promise(r => setTimeout(r, 5));
  expect(closes).toHaveBeenCalledTimes(1);
  cleanup();
  const second = render(<Harness silent={false} />);
  second.rerender(<Harness silent={false} sound={false} />);
  expect(closes).toHaveBeenCalledTimes(2);
});
