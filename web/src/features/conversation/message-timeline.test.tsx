import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { MessageTimeline } from "@/features/conversation/message-timeline";
import type {
  ConversationScope,
  DeliveryReceipt,
  LocalSendState,
  MediaTimelineEntry,
  TextTimelineEntry,
  TimelineEntry,
} from "@/features/conversation/types";

const scope: ConversationScope = {
  tenantId: "tenant-fixture",
  userId: "usuario-fixture",
  conversationId: "conversa-fixture",
};

function textEntry(
  id: string,
  text: string,
  options: { localState?: LocalSendState; receipt?: DeliveryReceipt; author?: string } = {},
): TextTimelineEntry {
  return {
    kind: "text",
    id,
    author: options.author ?? "Autora da fixture",
    text,
    localState: options.localState,
    receipt: options.receipt,
  };
}

function mediaEntry(id: string, localState: LocalSendState, caption: string, receipt?: DeliveryReceipt): MediaTimelineEntry {
  return {
    kind: "media",
    id,
    mediaKind: "image",
    author: "Autora da fixture",
    caption,
    filename: "foto.jpg",
    mime: "image/jpeg",
    voice: false,
    locationName: "",
    address: "",
    contacts: [],
    interactiveType: "",
    replyId: "",
    title: "",
    mediaStatus: "stored",
    mediaError: "",
    localState,
    receipt,
    attachment: { id: `${id}-file`, name: "foto.jpg", mediaType: "image/jpeg", state: "validated", bytes: 4 },
  };
}

function mockViewport() {
  const prototype = HTMLElement.prototype;
  const height = Object.getOwnPropertyDescriptor(prototype, "clientHeight");
  const width = Object.getOwnPropertyDescriptor(prototype, "clientWidth");
  Object.defineProperty(prototype, "clientHeight", {
    configurable: true,
    get: () => 480,
  });
  Object.defineProperty(prototype, "clientWidth", {
    configurable: true,
    get: () => 320,
  });
  return () => {
    if (height) Object.defineProperty(prototype, "clientHeight", height);
    else Reflect.deleteProperty(prototype, "clientHeight");
    if (width) Object.defineProperty(prototype, "clientWidth", width);
    else Reflect.deleteProperty(prototype, "clientWidth");
  };
}

function mockScrollBox() {
  const prototype = HTMLElement.prototype;
  const previous = {
    scrollTop: Object.getOwnPropertyDescriptor(prototype, "scrollTop"),
    scrollHeight: Object.getOwnPropertyDescriptor(prototype, "scrollHeight"),
    clientHeight: Object.getOwnPropertyDescriptor(prototype, "clientHeight"),
    clientWidth: Object.getOwnPropertyDescriptor(prototype, "clientWidth"),
  };
  let scrollTop = 0;
  Object.defineProperty(prototype, "scrollTop", {
    configurable: true,
    get: () => scrollTop,
    set: (value: number) => {
      scrollTop = Number(value);
    },
  });
  Object.defineProperty(prototype, "scrollHeight", {
    configurable: true,
    get: () => 2000,
  });
  Object.defineProperty(prototype, "clientHeight", {
    configurable: true,
    get: () => 400,
  });
  Object.defineProperty(prototype, "clientWidth", {
    configurable: true,
    get: () => 320,
  });
  return {
    read: () => scrollTop,
    set: (value: number) => {
      scrollTop = value;
    },
    restore: () => {
      for (const [key, descriptor] of Object.entries(previous)) {
        if (descriptor) Object.defineProperty(prototype, key, descriptor);
        else Reflect.deleteProperty(prototype, key);
      }
    },
  };
}

function mockMeasuredList(rowHeight: number, viewport: number, scrollHeight: number) {
  const prototype = HTMLElement.prototype;
  const previous = {
    scrollTop: Object.getOwnPropertyDescriptor(prototype, "scrollTop"),
    scrollHeight: Object.getOwnPropertyDescriptor(prototype, "scrollHeight"),
    clientHeight: Object.getOwnPropertyDescriptor(prototype, "clientHeight"),
    clientWidth: Object.getOwnPropertyDescriptor(prototype, "clientWidth"),
    offsetHeight: Object.getOwnPropertyDescriptor(prototype, "offsetHeight"),
    scrollTo: Object.getOwnPropertyDescriptor(prototype, "scrollTo"),
  };
  let scrollTop = 0;
  const maxScroll = Math.max(0, scrollHeight - viewport);
  Object.defineProperty(prototype, "scrollTop", {
    configurable: true,
    get: () => scrollTop,
    set: (value: number) => {
      scrollTop = Math.min(Math.max(0, Number(value)), maxScroll);
    },
  });
  Object.defineProperty(prototype, "scrollHeight", {
    configurable: true,
    get: () => scrollHeight,
  });
  Object.defineProperty(prototype, "clientHeight", {
    configurable: true,
    get: () => viewport,
  });
  Object.defineProperty(prototype, "clientWidth", {
    configurable: true,
    get: () => 320,
  });
  Object.defineProperty(prototype, "offsetHeight", {
    configurable: true,
    get: () => rowHeight,
  });
  Object.defineProperty(prototype, "scrollTo", {
    configurable: true,
    value(this: HTMLElement, options?: ScrollToOptions | number) {
      const top = typeof options === "number" ? options : (options?.top ?? 0);
      this.scrollTop = top;
      queueMicrotask(() => {
        this.dispatchEvent(new Event("scroll"));
      });
    },
  });
  return {
    read: () => scrollTop,
    restore: () => {
      for (const [key, descriptor] of Object.entries(previous)) {
        if (descriptor) Object.defineProperty(prototype, key, descriptor);
        else Reflect.deleteProperty(prototype, key);
      }
    },
  };
}

describe("linha do tempo", () => {
  let restore = () => undefined as void;

  afterEach(() => {
    restore();
    restore = () => undefined;
  });

  it("distingue aceitação, entrega, falha e incerteza", () => {
    restore = mockViewport();
    const entries: TimelineEntry[] = [
      textEntry("aceita", "texto aceito", { localState: "accepted" }),
      textEntry("entregue", "texto entregue", {
        localState: "accepted",
        receipt: "delivered",
      }),
      textEntry("lida", "texto lido", { receipt: "read" }),
      textEntry("falha", "texto com falha", { localState: "failed" }),
      textEntry("incerta", "texto incerto", { localState: "uncertain" }),
      textEntry("fila", "texto na fila", { localState: "queued" }),
      textEntry("andamento", "texto em andamento", { localState: "sending" }),
      textEntry("retentativa", "texto em retentativa", { localState: "retrying" }),
      textEntry("cancelada", "texto cancelado", { localState: "cancelled" }),
      {
        kind: "event",
        id: "evento",
        author: "Ator da fixture",
        summary: "Assumiu a conversa da fixture",
      },
      {
        kind: "unsupported",
        id: "tipo",
        author: "Canal da fixture",
        reference: "ref-fixture-1",
      },
    ];

    render(
      <MessageTimeline scope={scope} protocol="protocolo-fixture" status="ready" entries={entries} />,
    );

    expect(screen.getByRole("heading", { name: "Protocolo protocolo-fixture" })).toBeInTheDocument();
    const accepted = screen.getByRole("listitem", { name: /texto aceito/ });
    expect(within(accepted).getByText("Aceita pelo conector")).toHaveAttribute(
      "data-local-state",
      "accepted",
    );
    expect(within(accepted).queryByText("Entregue ao destinatário")).not.toBeInTheDocument();
    expect(within(accepted).queryByText("Envio não confirmado")).not.toBeInTheDocument();

    const delivered = screen.getByRole("listitem", { name: /texto entregue/ });
    expect(within(delivered).getByText("Aceita pelo conector")).toHaveAttribute(
      "data-local-state",
      "accepted",
    );
    expect(within(delivered).getByText("Entregue ao destinatário")).toHaveAttribute(
      "data-receipt",
      "delivered",
    );

    const read = screen.getByRole("listitem", { name: /texto lido/ });
    expect(within(read).getByText("Lida pelo destinatário")).toHaveAttribute("data-receipt", "read");
    expect(within(read).queryByText("Aceita pelo conector")).not.toBeInTheDocument();
    expect(within(read).queryByText("Entregue ao destinatário")).not.toBeInTheDocument();

    const failed = screen.getByRole("listitem", { name: /texto com falha/ });
    expect(within(failed).getByText("Falha no envio")).toHaveAttribute("data-local-state", "failed");
    expect(within(failed).queryByText("Aceita pelo conector")).not.toBeInTheDocument();
    expect(within(failed).queryByText("Entregue ao destinatário")).not.toBeInTheDocument();

    const uncertain = screen.getByRole("listitem", { name: /texto incerto/ });
    expect(within(uncertain).getByText("Envio não confirmado")).toHaveAttribute(
      "data-local-state",
      "uncertain",
    );
    expect(within(uncertain).queryByText("Aceita pelo conector")).not.toBeInTheDocument();
    expect(within(uncertain).queryByText("Falha no envio")).not.toBeInTheDocument();

    expect(screen.getByText("Na fila local")).toBeInTheDocument();
    expect(screen.getByText("Envio em andamento")).toBeInTheDocument();
    expect(screen.getByText("Retentativa segura")).toBeInTheDocument();
    expect(screen.getByText("Cancelada antes do envio")).toBeInTheDocument();
    expect(screen.getByText("Assumiu a conversa da fixture")).toBeInTheDocument();
    expect(screen.getByText("Autor: Ator da fixture")).toBeInTheDocument();
    expect(screen.getByText("Referência: ref-fixture-1")).toBeInTheDocument();
    expect(screen.getByText("O painel não interpreta este conteúdo.")).toBeInTheDocument();
    expect(screen.getAllByText("Tipo não suportado").length).toBeGreaterThan(0);
  });

  it("não interpreta HTML ativo do texto recebido", () => {
    restore = mockViewport();
    const hostile =
      '<img alt="ativo" src="x" onerror="alert(1)"><script>alert(1)</script><a href="javascript:alert(1)">link</a>';
    render(
      <MessageTimeline scope={scope}
        protocol="protocolo-fixture"
        status="ready"
        entries={[
          textEntry("hostil", hostile),
          {
            kind: "unsupported",
            id: "ref-hostil",
            author: "Canal da fixture",
            reference: '<svg><script>alert(1)</script></svg>',
          },
        ]}
      />,
    );

    const history = screen.getByRole("region", { name: "Histórico da conversa" });
    expect(history.querySelector("img, script, a, svg")).toBeNull();
    expect(screen.getByText(hostile)).toBeInTheDocument();
    expect(screen.getByText("Referência: <svg><script>alert(1)</script></svg>")).toBeInTheDocument();
  });

  it("mostra carregamento, erro e vazio sem inventar mensagens", () => {
    restore = mockViewport();
    const { rerender } = render(
      <MessageTimeline scope={scope} protocol="protocolo-fixture" status="loading" entries={[]} />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Carregando o histórico.");
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();

    rerender(<MessageTimeline scope={scope} protocol="protocolo-fixture" status="error" entries={[]} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Não foi possível carregar o histórico.");
    expect(alert).toHaveTextContent("Nenhuma mensagem foi inventada.");

    rerender(<MessageTimeline scope={scope} protocol="protocolo-fixture" status="ready" entries={[]} />);
    expect(screen.getByText("Nenhuma mensagem nesta conversa.")).toBeInTheDocument();
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("mostra fila, falha, cancelamento e incerteza da mídia sem tratar o arquivo como entrega", () => {
    restore = mockViewport();
    render(
      <MessageTimeline
        scope={scope}
        protocol="protocolo-fixture"
        status="ready"
        entries={[
          mediaEntry("fila", "queued", "mídia na fila"),
          mediaEntry("falha", "failed", "mídia com falha"),
          mediaEntry("cancelada", "cancelled", "mídia cancelada"),
          mediaEntry("incerta", "uncertain", "mídia incerta"),
          mediaEntry("entregue", "accepted", "mídia entregue", "delivered"),
        ]}
      />,
    );

    const queued = screen.getByRole("listitem", { name: /mídia na fila/ });
    expect(within(queued).getByText("Na fila local")).toHaveAttribute("data-local-state", "queued");
    expect(within(queued).getAllByText("Imagem").length).toBeGreaterThan(0);
    expect(within(queued).queryByText("Entregue ao destinatário")).not.toBeInTheDocument();

    expect(within(screen.getByRole("listitem", { name: /mídia com falha/ })).getByText("Falha no envio")).toHaveAttribute("data-local-state", "failed");
    expect(within(screen.getByRole("listitem", { name: /mídia cancelada/ })).getByText("Cancelada antes do envio")).toHaveAttribute("data-local-state", "cancelled");
    expect(within(screen.getByRole("listitem", { name: /mídia incerta/ })).getByText("Envio não confirmado")).toHaveAttribute("data-local-state", "uncertain");

    const delivered = screen.getByRole("listitem", { name: /mídia entregue/ });
    expect(within(delivered).getByText("Aceita pelo conector")).toHaveAttribute("data-local-state", "accepted");
    expect(within(delivered).getByText("Entregue ao destinatário")).toHaveAttribute("data-receipt", "delivered");
  });

  it("não arrasta a leitura antiga e volta ao fim pelo botão", async () => {
    const user = userEvent.setup();
    const box = mockScrollBox();
    restore = box.restore;
    const initial = [textEntry("1", "primeira"), textEntry("2", "segunda")];
    const { rerender } = render(
      <MessageTimeline scope={scope} protocol="protocolo-fixture" status="ready" entries={initial} />,
    );

    const history = screen.getByRole("region", { name: "Histórico da conversa" });
    box.set(0);
    history.dispatchEvent(new Event("scroll", { bubbles: true }));

    expect(await screen.findByRole("button", { name: "Voltar ao fim" })).toBeInTheDocument();
    expect(box.read()).toBe(0);

    rerender(
      <MessageTimeline scope={scope}
        protocol="protocolo-fixture"
        status="ready"
        entries={[...initial, textEntry("3", "terceira"), textEntry("4", "quarta")]}
      />,
    );

    expect(box.read()).toBe(0);
    expect(screen.getByRole("status")).toHaveTextContent("2 mensagens novas");
    await user.click(screen.getByRole("button", { name: "Voltar ao fim" }));
    expect(box.read()).toBe(1600);
    expect(screen.queryByRole("button", { name: "Voltar ao fim" })).not.toBeInTheDocument();
    expect(screen.queryByText("2 mensagens novas")).not.toBeInTheDocument();
  });

  it("move o foco com o teclado", async () => {
    const user = userEvent.setup();
    restore = mockViewport();
    render(
      <MessageTimeline scope={scope}
        protocol="protocolo-fixture"
        status="ready"
        entries={[
          textEntry("1", "primeira"),
          textEntry("2", "segunda"),
          textEntry("3", "terceira"),
        ]}
      />,
    );

    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveAttribute("aria-posinset", "1");
    expect(items[0]).toHaveAttribute("aria-setsize", "3");
    expect(items[2]).toHaveAttribute("aria-posinset", "3");
    expect(items[2]).toHaveAttribute("aria-setsize", "3");
    items[0]?.focus();
    expect(items[0]).toHaveFocus();

    await user.keyboard("{ArrowDown}");
    expect(items[1]).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(items[2]).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(items[2]).toHaveFocus();
    await user.keyboard("{Home}");
    expect(items[0]).toHaveFocus();
    await user.keyboard("{End}");
    expect(items[2]).toHaveFocus();
    await user.keyboard("{ArrowUp}");
    expect(items[1]).toHaveFocus();
  });

  it("mede a linha pelo conteúdo e não fixa a altura", () => {
    const box = mockMeasuredList(220, 480, 220);
    restore = box.restore;
    render(
      <MessageTimeline
        scope={scope}
        protocol="protocolo-fixture"
        status="ready"
        entries={[textEntry("longo", "a".repeat(200))]}
      />,
    );

    const item = screen.getByRole("listitem");
    expect(item).toHaveAttribute("data-index", "0");
    expect(item.style.height).toBe("");
    expect(screen.getByRole("list", { name: "Mensagens e eventos" })).toHaveStyle({
      height: "220px",
    });
  });

  it("rola e foca Home e End quando o alvo está fora da janela", async () => {
    const user = userEvent.setup();
    const rowHeight = 100;
    const viewport = 400;
    const count = 40;
    const box = mockMeasuredList(rowHeight, viewport, count * rowHeight);
    restore = box.restore;
    const entries = Array.from({ length: count }, (_, index) =>
      textEntry(`id-${index}`, `marcador-${index}`),
    );
    render(
      <MessageTimeline scope={scope} protocol="protocolo-fixture" status="ready" entries={entries} />,
    );

    const history = screen.getByRole("region", { name: "Histórico da conversa" });
    await act(async () => {
      history.dispatchEvent(new Event("scroll"));
    });

    const last = await screen.findByRole("listitem", { name: /marcador-39$/ });
    await user.click(last);
    expect(screen.queryByRole("listitem", { name: /marcador-0$/ })).not.toBeInTheDocument();

    await user.keyboard("{Home}");
    const first = screen.getByRole("listitem", { name: /marcador-0$/ });
    expect(first).toHaveFocus();
    expect(first).toHaveAttribute("aria-posinset", "1");
    expect(first).toHaveAttribute("aria-setsize", "40");
    expect(box.read()).toBe(0);

    await user.keyboard("{End}");
    const lastAgain = screen.getByRole("listitem", { name: /marcador-39$/ });
    expect(lastAgain).toHaveFocus();
    expect(lastAgain).toHaveAttribute("aria-posinset", "40");
    expect(lastAgain).toHaveAttribute("aria-setsize", "40");
    expect(box.read()).toBe(count * rowHeight - viewport);

    history.scrollTop = 0;
    await act(async () => {
      history.dispatchEvent(new Event("scroll"));
    });
    expect(screen.getByRole("listitem", { name: /marcador-39$/ })).toHaveAttribute("tabindex", "0");
  });

  it("reseta a leitura ao trocar o escopo da conversa", async () => {
    const box = mockScrollBox();
    restore = box.restore;
    const initial = [textEntry("1", "primeira"), textEntry("2", "segunda")];
    const { rerender } = render(
      <MessageTimeline scope={scope} protocol="protocolo-fixture" status="ready" entries={initial} />,
    );

    const history = screen.getByRole("region", { name: "Histórico da conversa" });
    box.set(0);
    history.dispatchEvent(new Event("scroll", { bubbles: true }));
    expect(await screen.findByRole("button", { name: "Voltar ao fim" })).toBeInTheDocument();

    rerender(
      <MessageTimeline
        scope={{ ...scope, conversationId: "conversa-seguinte" }}
        protocol="protocolo-fixture"
        status="ready"
        entries={[...initial, textEntry("3", "terceira")]}
      />,
    );

    expect(screen.queryByRole("button", { name: "Voltar ao fim" })).not.toBeInTheDocument();
    expect(screen.queryByText(/mensagens novas/)).not.toBeInTheDocument();
    expect(box.read()).toBe(1600);
  });
});
