import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { VirtualItemList } from "@/components/layout/virtual-item-list";

function mockViewportSize() {
  const elementPrototype = HTMLElement.prototype;
  const height = Object.getOwnPropertyDescriptor(elementPrototype, "clientHeight");
  const width = Object.getOwnPropertyDescriptor(elementPrototype, "clientWidth");
  Object.defineProperty(elementPrototype, "clientHeight", {
    configurable: true,
    get: () => 480,
  });
  Object.defineProperty(elementPrototype, "clientWidth", {
    configurable: true,
    get: () => 320,
  });

  return () => {
    if (height) {
      Object.defineProperty(elementPrototype, "clientHeight", height);
    } else {
      Reflect.deleteProperty(elementPrototype, "clientHeight");
    }
    if (width) {
      Object.defineProperty(elementPrototype, "clientWidth", width);
    } else {
      Reflect.deleteProperty(elementPrototype, "clientWidth");
    }
  };
}

describe("lista virtual", () => {
  let restoreViewport = () => undefined as void;

  afterEach(() => {
    restoreViewport();
  });

  it("mostra os rótulos recebidos", () => {
    restoreViewport = mockViewportSize();
    render(
      <VirtualItemList
        items={[
          { id: "1", label: "Primeira linha" },
          { id: "2", label: "Segunda linha" },
        ]}
      />,
    );

    expect(screen.getByRole("list", { name: "Registros da área" })).toBeInTheDocument();
    expect(screen.getByText("Primeira linha")).toBeInTheDocument();
    expect(screen.getByText("Segunda linha")).toBeInTheDocument();
  });
});
