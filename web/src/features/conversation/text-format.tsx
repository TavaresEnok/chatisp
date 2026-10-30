import type { ReactNode } from "react";

export function formattedText(text: string): ReactNode[] {
  const parts: ReactNode[] = [];
  const pattern = /\*([^*\n]+)\*|_([^_\n]+)_/g;
  let end = 0;
  for (const match of text.matchAll(pattern)) {
    const index = match.index;
    parts.push(text.slice(end, index));
    parts.push(match[1] !== undefined ? <strong key={index}>{match[1]}</strong> : <em key={index}>{match[2]}</em>);
    end = index + match[0].length;
  }
  parts.push(text.slice(end));
  return parts;
}
