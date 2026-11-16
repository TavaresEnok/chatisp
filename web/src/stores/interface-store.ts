import { create } from "zustand";

export type InterfaceTheme = "dark" | "light";

const themeKey = "chatisp-interface-theme";
const menuKey = "chatisp-interface-menu";
const queueWidthKey = "chatisp-interface-queue-width";
const detailsKey = "chatisp-interface-details";

function validQueueWidth(value: number) {
  return Number.isFinite(value) && value >= 280 && value <= 400 ? value : 320;
}

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Sem armazenamento local: a preferência vale só nesta aba.
  }
}

type InterfaceState = {
  theme: InterfaceTheme;
  menuExpanded: boolean;
  queueWidth: number;
  detailsExpanded: boolean;
  setQueueWidth: (width: number) => void;
  setDetailsExpanded: (expanded: boolean) => void;
  toggleTheme: () => void;
  setMenuExpanded: (expanded: boolean) => void;
};

/*
  Preferências de aparência deste dispositivo: tema e menu lateral expandido.
  Não guardam dado do servidor. A limpeza do armazenamento ao sair da sessão
  também as apaga; nada é gravado enquanto a pessoa não alterar uma delas.
*/
export const useInterfaceStore = create<InterfaceState>((set, get) => ({
  theme: read(themeKey) === "light" ? "light" : "dark",
  menuExpanded: read(menuKey) === "expanded",
  queueWidth: validQueueWidth(Number(read(queueWidthKey))),
  detailsExpanded: read(detailsKey) !== "collapsed",
  setQueueWidth: (width) => {
    const queueWidth = validQueueWidth(width);
    write(queueWidthKey, String(queueWidth));
    set({ queueWidth });
  },
  setDetailsExpanded: (detailsExpanded) => {
    write(detailsKey, detailsExpanded ? "expanded" : "collapsed");
    set({ detailsExpanded });
  },
  toggleTheme: () => {
    const theme = get().theme === "dark" ? "light" : "dark";
    write(themeKey, theme);
    set({ theme });
  },
  setMenuExpanded: (menuExpanded) => {
    write(menuKey, menuExpanded ? "expanded" : "collapsed");
    set({ menuExpanded });
  },
}));

export function resetInterfaceStore() {
  useInterfaceStore.setState({ theme: "dark", menuExpanded: false, queueWidth: 320, detailsExpanded: true });
}

function apply(state: InterfaceState) {
  const root = document.documentElement;
  root.classList.toggle("dark", state.theme === "dark");
  root.dataset.menu = state.menuExpanded ? "expanded" : "collapsed";
}

export function syncInterfaceToDocument() {
  apply(useInterfaceStore.getState());
  return useInterfaceStore.subscribe(apply);
}
