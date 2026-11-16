import { ChevronRight } from "lucide-react";
import { useRef } from "react";
import { useInterfaceStore } from "@/stores/interface-store";
import {
  HANDLE_COLUMN_EDGE,
  HANDLE_FILL,
  HANDLE_GAP,
  HANDLE_RAIL_EDGE,
} from "@/components/layout/nav-handle-paths";

/*
  Aba de expandir o menu lateral. O SVG redesenha um trecho das bordas da
  pílula do menu e do painel ao lado: a pílula avança 7 px e o painel recua
  com a mesma curva, mantendo o vão de 8 px entre os dois.
  Clicar alterna; arrastar para a direita abre e para a esquerda fecha.
*/
export function NavHandle() {
  const expanded = useInterfaceStore((state) => state.menuExpanded);
  const setExpanded = useInterfaceStore((state) => state.setMenuExpanded);
  const drag = useRef<{ x: number; moved: boolean } | null>(null);
  const skipClick = useRef(false);
  const label = expanded ? "Recolher menu" : "Expandir menu";

  return (
    <div className="nav-handle">
      <svg viewBox="0 0 60 100" aria-hidden="true" focusable="false">
        <path className="nav-handle-fill" d={HANDLE_FILL} />
        <path className="nav-handle-gap" d={HANDLE_GAP} />
        <path className="nav-handle-under" d={HANDLE_RAIL_EDGE} />
        <path className="nav-handle-line" d={HANDLE_RAIL_EDGE} />
        <path className="nav-handle-under" d={HANDLE_COLUMN_EDGE} />
        <path className="nav-handle-line" d={HANDLE_COLUMN_EDGE} />
      </svg>
      <button
        type="button"
        className="nav-handle-button"
        aria-expanded={expanded}
        aria-controls="app-navigation"
        aria-label={label}
        title={`${label} ( [ )`}
        onClick={() => {
          if (skipClick.current) {
            skipClick.current = false;
            return;
          }
          setExpanded(!expanded);
        }}
        onPointerDown={(event) => {
          drag.current = { x: event.clientX, moved: false };
          try {
            event.currentTarget.setPointerCapture(event.pointerId);
          } catch {
            // Sem captura de ponteiro, o clique continua funcionando.
          }
        }}
        onPointerMove={(event) => {
          const current = drag.current;
          if (!current) return;
          const distance = event.clientX - current.x;
          const open = useInterfaceStore.getState().menuExpanded;
          if ((distance > 12 && !open) || (distance < -12 && open)) {
            setExpanded(!open);
            current.moved = true;
            current.x = event.clientX;
          }
        }}
        onPointerUp={() => {
          if (drag.current?.moved) {
            skipClick.current = true;
            window.setTimeout(() => {
              skipClick.current = false;
            }, 60);
          }
          drag.current = null;
        }}
        onPointerCancel={() => {
          drag.current = null;
        }}
      >
        <ChevronRight aria-hidden="true" />
      </button>
    </div>
  );
}
