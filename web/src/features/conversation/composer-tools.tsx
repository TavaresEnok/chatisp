import type { RefObject } from "react";

export function ComposerTools({ field, value, onChange }: { field: RefObject<HTMLTextAreaElement | null>; value: string; onChange: (value: string) => void }) {
  function insert(before: string, after = "") {
    const node = field.current;
    if (!node) return;
    const start = node.selectionStart;
    const end = node.selectionEnd;
    onChange(value.slice(0, start) + before + value.slice(start, end) + after + value.slice(end));
    requestAnimationFrame(() => {
      node.focus();
      node.setSelectionRange(start + before.length, end + before.length);
    });
  }
  return <div role="toolbar" aria-label="Ferramentas de mensagem" className="flex flex-wrap gap-2">
    <button type="button" onClick={() => insert("*", "*")}>Negrito</button>
    <button type="button" onClick={() => insert("_", "_")}>Itálico</button>
    {["😀", "😊", "👍", "❤️"].map((emoji) => <button type="button" key={emoji} aria-label={`Inserir ${emoji}`} onClick={() => insert(emoji)}>{emoji}</button>)}
  </div>;
}
