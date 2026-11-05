import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ContactAvatar, contactHue, contactInitials } from "./contact-avatar";

it("gera iniciais estáveis, com acento e sem texto", () => {
  expect(contactInitials("Ana")).toBe("A");
  expect(contactInitials("Ana Paula Souza")).toBe("AS");
  expect(contactInitials("ângela  silva")).toBe("ÂS");
  expect(contactInitials("+5511999999999")).toBe("5");
  expect(contactInitials("   ")).toBe("?");
  expect(contactInitials("")).toBe("?");
  expect(contactInitials("😀 Zé")).toBe("Z");
});

it("a cor é determinística e independe de caixa", () => {
  expect(contactHue("Ana Souza")).toBe(contactHue("ana souza"));
  expect(contactHue("Ana Souza")).toBeGreaterThanOrEqual(0);
  expect(contactHue("Ana Souza")).toBeLessThan(360);
});

it("é decorativo: fora da árvore de acessibilidade e sem imagem remota", () => {
  const { container } = render(<ContactAvatar label="Joana Lima" />);
  const el = screen.getByTestId("contact-avatar");
  expect(el).toHaveAttribute("aria-hidden", "true");
  expect(el).toHaveTextContent("JL");
  expect(container.querySelector("img")).toBeNull();
});
