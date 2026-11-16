import { expect, it, vi } from "vitest";
import { clearBrowserStorage } from "@/lib/browser-storage";
import { syncInterfaceToDocument, useInterfaceStore } from "@/stores/interface-store";

it("redefine aparência em memória e no documento sem gravar preferências ao limpar a sessão", () => {
  const unsubscribe = syncInterfaceToDocument();
  useInterfaceStore.setState({ theme: "light", menuExpanded: true });
  localStorage.setItem("chatisp-interface-theme", "light");
  localStorage.setItem("chatisp-interface-menu", "expanded");
  const write = vi.spyOn(Storage.prototype, "setItem");
  clearBrowserStorage();
  expect(useInterfaceStore.getState()).toMatchObject({ theme: "dark", menuExpanded: false });
  expect(document.documentElement).toHaveClass("dark");
  expect(document.documentElement.dataset.menu).toBe("collapsed");
  expect(localStorage.getItem("chatisp-interface-theme")).toBeNull();
  expect(localStorage.getItem("chatisp-interface-menu")).toBeNull();
  expect(write).not.toHaveBeenCalled();
  write.mockRestore();
  unsubscribe();
});
