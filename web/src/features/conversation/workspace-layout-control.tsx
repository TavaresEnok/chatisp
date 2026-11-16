import { Columns2 } from "lucide-react";
import { useRef } from "react";
import { useInterfaceStore } from "@/stores/interface-store";

export function WorkspaceLayoutControl() {
  const width = useInterfaceStore(s => s.queueWidth);
  const setWidth = useInterfaceStore(s => s.setQueueWidth);
  const details = useRef<HTMLDetailsElement>(null);
  return <details ref={details} className="workspace-layout-control" onBlur={event => {
    if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false;
  }} onKeyDown={event => {
    if (event.key === "Escape" && details.current) {
      event.preventDefault();
      details.current.open = false;
      details.current.querySelector("summary")?.focus();
    }
  }}>
    <summary title="Ajustar layout" aria-label="Ajustar layout"><Columns2 size={17} aria-hidden="true" /></summary>
    <div className="workspace-layout-options">
      <label>Largura da fila <span>{width} px</span><input type="range" min={280} max={400} step={10} value={width} onChange={event => setWidth(Number(event.target.value))} /></label>
      <p>Largura preferida, limitada ao espaço da tela. Salva neste dispositivo.</p>
      <button type="button" onClick={() => setWidth(320)}>Restaurar largura</button>
    </div>
  </details>;
}
