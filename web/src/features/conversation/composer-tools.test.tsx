import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef, useState } from "react";
import { describe, expect, it } from "vitest";
import { ComposerTools } from "./composer-tools";

function Harness({ initial }: { initial: string }) {
  const [value, setValue] = useState(initial);
  const field = createRef<HTMLTextAreaElement>();
  return <>
    <textarea ref={field} aria-label="texto" value={value} onChange={(event) => setValue(event.target.value)} />
    <ComposerTools field={field} value={value} onChange={setValue} />
  </>;
}

describe("ComposerTools", () => {
  it("envolve a seleção com a sintaxe do WhatsApp e insere emoji", async () => {
    const user = userEvent.setup();
    render(<Harness initial="olá cliente" />);
    const field = screen.getByLabelText("texto") as HTMLTextAreaElement;
    field.setSelectionRange(4, 11);
    await user.click(screen.getByRole("button", { name: "Negrito" }));
    expect(field.value).toBe("olá *cliente*");
    await new Promise((resolve) => requestAnimationFrame(() => resolve(null)));
    field.setSelectionRange(0, 3);
    await user.click(screen.getByRole("button", { name: "Itálico" }));
    expect(field.value).toBe("_olá_ *cliente*");
    await user.click(screen.getByRole("button", { name: "Inserir 👍" }));
    expect(field.value).toContain("👍");
  });
});
