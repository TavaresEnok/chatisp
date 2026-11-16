import { resetInterfaceStore } from "@/stores/interface-store";

export function clearBrowserStorage() {
  resetInterfaceStore();
  try {
    localStorage.clear();
  } catch {
    // Ambiente sem armazenamento local.
  }
  try {
    sessionStorage.clear();
  } catch {
    // Ambiente sem armazenamento de sessão.
  }
}
