import { X } from "lucide-react";
import { useId, useRef } from "react";

export type QueueSearchProps = {
  value: string;
  onChange: (value: string) => void;
};

export function QueueSearch({ value, onChange }: QueueSearchProps) {
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  return (
    <div className="queue-search flex flex-wrap items-end gap-2">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <label htmlFor={id}>Buscar na fila</label>
        <input ref={inputRef} id={id} type="search" value={value}
          placeholder="Nome, telefone, protocolo ou conteúdo"
          className="h-11 rounded-lg border border-input px-2"
          onChange={(event) => onChange(event.target.value)} />
      </div>
      <button type="button" aria-label="Limpar busca" title="Limpar busca" disabled={!value} className="min-h-11 rounded-lg border px-3 disabled:opacity-50"
        onClick={() => { onChange(""); inputRef.current?.focus(); }}>
        <X size={16} aria-hidden="true" />
      </button>
    </div>
  );
}
