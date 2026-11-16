import { Bold, Italic } from "lucide-react";
import type { RefObject } from "react";

const toolButton = "composer-icon";

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
  return <div role="toolbar" aria-label="Ferramentas de mensagem" className="composer-format">
    <button type="button" className={toolButton} title="Negrito" onClick={() => insert("*", "*")}><Bold size={16} strokeWidth={1.8} aria-hidden="true" /><span className="visually-hidden">Negrito</span></button>
    <button type="button" className={toolButton} title="Itálico" onClick={() => insert("_", "_")}><Italic size={16} strokeWidth={1.8} aria-hidden="true" /><span className="visually-hidden">Itálico</span></button>
    {["😀", "😊", "👍", "❤️"].map((emoji) => <button type="button" className={`${toolButton} composer-emoji`} key={emoji} aria-label={`Inserir ${emoji}`} title={`Inserir ${emoji}`} onClick={() => insert(emoji)}>{emoji}</button>)}
  </div>;
}
