import { expect, it, vi } from "vitest";
import { clearBrowserStorage } from "@/lib/browser-storage";
import { syncInterfaceToDocument, useInterfaceStore } from "@/stores/interface-store";

it("redefine aparência em memória e no documento sem gravar preferências ao limpar a sessão", () => {
  const unsubscribe = syncInterfaceToDocument();
  useInterfaceStore.setState({ theme: "light", menuExpanded: true, queueWidth: 380, detailsExpanded: false });
  localStorage.setItem("chatisp-interface-theme", "light");
  localStorage.setItem("chatisp-interface-menu", "expanded");
  localStorage.setItem("chatisp-interface-queue-width", "380");
  localStorage.setItem("chatisp-interface-details", "collapsed");
  const write = vi.spyOn(Storage.prototype, "setItem");
  clearBrowserStorage();
  expect(useInterfaceStore.getState()).toMatchObject({ theme: "dark", menuExpanded: false, queueWidth: 320, detailsExpanded: true });
  expect(document.documentElement).toHaveClass("dark");
  expect(document.documentElement.dataset.menu).toBe("collapsed");
  expect(localStorage.getItem("chatisp-interface-theme")).toBeNull();
  expect(localStorage.getItem("chatisp-interface-menu")).toBeNull();
  expect(localStorage.getItem("chatisp-interface-queue-width")).toBeNull();
  expect(localStorage.getItem("chatisp-interface-details")).toBeNull();
  expect(write).not.toHaveBeenCalled();
  write.mockRestore();
  unsubscribe();
});

it("salva a largura escolhida e limita valores inválidos sem armazenar dados do atendimento", () => {
  useInterfaceStore.getState().setQueueWidth(380);
  expect(localStorage.getItem("chatisp-interface-queue-width")).toBe("380");
  useInterfaceStore.getState().setQueueWidth(900);
  expect(useInterfaceStore.getState().queueWidth).toBe(320);
  useInterfaceStore.getState().setDetailsExpanded(false);
  expect(localStorage.getItem("chatisp-interface-details")).toBe("collapsed");
  clearBrowserStorage();
});
