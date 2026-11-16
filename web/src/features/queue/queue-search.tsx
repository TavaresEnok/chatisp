import { Search, X } from "lucide-react";
import { useId, useRef } from "react";

export type QueueSearchProps = {
  value: string;
  onChange: (value: string) => void;
};

export function QueueSearch({ value, onChange }: QueueSearchProps) {
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  return (
    <div className="queue-search">
      <Search size={16} strokeWidth={1.6} aria-hidden="true" />
      <label htmlFor={id} className="visually-hidden">Buscar na fila</label>
      <input ref={inputRef} id={id} type="search" value={value}
        placeholder="Nome, telefone, protocolo ou conteúdo"
        onChange={(event) => onChange(event.target.value)} />
      <button type="button" aria-label="Limpar busca" title="Limpar busca" disabled={!value}
        onClick={() => { onChange(""); inputRef.current?.focus(); }}>
        <X size={14} aria-hidden="true" />
      </button>
    </div>
  );
}
