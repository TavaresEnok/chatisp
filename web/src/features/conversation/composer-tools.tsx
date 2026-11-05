import type { RefObject } from "react";

const toolButton = "inline-flex min-h-11 min-w-11 items-center justify-center rounded-lg border border-input px-3 text-sm text-foreground hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground";

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
    <button type="button" className={toolButton} onClick={() => insert("*", "*")}>Negrito</button>
    <button type="button" className={toolButton} onClick={() => insert("_", "_")}>Itálico</button>
    {["😀", "😊", "👍", "❤️"].map((emoji) => <button type="button" className={toolButton} key={emoji} aria-label={`Inserir ${emoji}`} onClick={() => insert(emoji)}>{emoji}</button>)}
  </div>;
}
