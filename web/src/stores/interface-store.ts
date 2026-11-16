import { create } from "zustand";

export type InterfaceTheme = "dark" | "light";

const themeKey = "chatisp-interface-theme";
const menuKey = "chatisp-interface-menu";

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

function apply(state: InterfaceState) {
  const root = document.documentElement;
  root.classList.toggle("dark", state.theme === "dark");
  root.dataset.menu = state.menuExpanded ? "expanded" : "collapsed";
}

export function syncInterfaceToDocument() {
  apply(useInterfaceStore.getState());
  return useInterfaceStore.subscribe(apply);
}
