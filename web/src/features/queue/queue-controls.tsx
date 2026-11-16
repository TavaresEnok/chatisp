import { Clock, Flag, SlidersHorizontal } from "lucide-react";
import { ContactAvatar } from "@/components/ui/contact-avatar";
import { useEffect, useId, useRef } from "react";
import type { KeyboardEvent } from "react";
import { QueueSearch } from "./queue-search";
import type { QueueFilterOptions, QueueFilters, QueueItem, QueueOrder, QueueResultState, QueueTab } from "./queue-types";

export type QueueControlsProps = {
  counts?: Partial<Record<QueueTab, number>>;
  tab: QueueTab;
  onTabChange: (tab: QueueTab) => void;
  search: string;
  onSearchChange: (value: string) => void;
  filters: QueueFilters;
  filterOptions: QueueFilterOptions;
  onFiltersChange: (filters: QueueFilters) => void;
  order: QueueOrder;
  onOrderChange: (order: QueueOrder) => void;
  resultState: QueueResultState;
  items: readonly QueueItem[];
  selectedId: string | null;
  onSelect: (id: string) => void;
};

const tabs: readonly { value: QueueTab; label: string }[] = [
  { value: "mine", label: "Minhas" },
  { value: "unassigned", label: "Não atribuídas" },
  { value: "all", label: "Todas" },
];
const fields = [
  { key: "status", label: "Status" },
  { key: "sector", label: "Setor" },
  { key: "assignee", label: "Atendente" },
  { key: "inbox", label: "Caixa" },
  { key: "tag", label: "Tag" },
] as const;

function messageTime(value: string | undefined): string | null {
  if (!value) return null;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime()) || date.getFullYear() < 1970) return null;
  return new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }).format(date);
}

/** Hoje mostra só a hora; em outros dias, dia e mês. A data completa fica na dica do elemento. */
function compactTime(value: string | undefined): string | null {
  if (!value || !messageTime(value)) return null;
  const date = new Date(value);
  const now = new Date();
  const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
  return new Intl.DateTimeFormat("pt-BR", sameDay ? { hour: "2-digit", minute: "2-digit" } : { day: "2-digit", month: "2-digit" }).format(date);
}

/** Só para o ponto ao lado do estado: a forma reforça o texto, que continua sendo a informação. */
const stateMark: Record<string, string> = { "Em triagem": "triage", Aberta: "open", "Aguardando cliente": "waiting_customer", Encerrada: "closed" };

/** Um e-mail como rótulo ocupa a linha toda: mostra a parte antes do @ e deixa o valor inteiro na dica. */
function shortLabel(label: string): string {
  return /^[^\s@]+@[^\s@]+$/.test(label) ? label.slice(0, label.indexOf("@")) : label;
}

export function QueueControls(props: QueueControlsProps) {
  const id = useId();
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const filterBox = useRef<HTMLDetailsElement>(null);
  // Os filtros abrem sobre a lista. Clicar fora ou apertar Esc fecha, como em qualquer menu.
  useEffect(() => {
    function close(event: Event) {
      const box = filterBox.current;
      if (!box?.open) return;
      if (event instanceof KeyboardEvent) {
        if (event.key !== "Escape") return;
        box.open = false;
        box.querySelector("summary")?.focus();
        return;
      }
      if (event.target instanceof Node && !box.contains(event.target)) box.open = false;
    }
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", close);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", close);
    };
  }, []);
  function changeTab(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    let next: number;
    switch (event.key) {
      case "ArrowRight": next = (index + 1) % tabs.length; break;
      case "ArrowLeft": next = (index + tabs.length - 1) % tabs.length; break;
      case "Home": next = 0; break;
      case "End": next = tabs.length - 1; break;
      default: return;
    }
    event.preventDefault();
    const tab = tabs[next];
    if (tab) { props.onTabChange(tab.value); tabRefs.current[next]?.focus(); }
  }
  function setFilter(key: keyof QueueFilters, value: string) {
    props.onFiltersChange({ ...props.filters, [key]: value });
  }
  function clearFilters() {
    props.onFiltersChange({ status: "", sector: "", assignee: "", inbox: "", tag: "", from: "", to: "" });
  }
  const activeFilters = Object.values(props.filters).some(Boolean);
  return (
    <section aria-label="Fila de conversas" className="queue-controls">
      <div className="queue-heading visually-hidden"><h2>Atendimentos</h2>{props.resultState === "ready" && <span title={props.counts ? "Total no recorte autorizado" : "Resultados desta página"}>{props.counts?.[props.tab] ?? props.items.length}</span>}</div>
      <div className="queue-tools">
        <QueueSearch value={props.search} onChange={props.onSearchChange} />
        <details ref={filterBox} className="queue-filters" data-active={activeFilters ? "true" : undefined}>
          <summary title="Filtros e ordenação"><SlidersHorizontal size={16} strokeWidth={1.6} aria-hidden="true" /><span className="visually-hidden">Filtrar atendimentos{activeFilters ? " • ativos" : ""}</span></summary>
          <div className="queue-filter-panel">
            <fieldset>
              <legend>Filtros</legend>
              {fields.map(({ key, label }) => (
                <label key={key}>{label}
                  <select value={props.filters[key]} onChange={(event) => setFilter(key, event.target.value)}>
                    <option value="">Todos</option>
                    {props.filterOptions[key].map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </select>
                </label>
              ))}
              <label>De
                <input type="date" value={props.filters.from} onChange={(event) => setFilter("from", event.target.value)} />
              </label>
              <label>Até
                <input type="date" value={props.filters.to} onChange={(event) => setFilter("to", event.target.value)} />
              </label>
            </fieldset>
            <label className="queue-order">Ordenar por
              <select value={props.order} onChange={(event) => props.onOrderChange(event.target.value as QueueOrder)}>
                <option value="wait">Tempo de espera</option>
                <option value="lastMessage">Última mensagem</option>
              </select>
            </label>
            <button type="button" className="queue-clear" onClick={clearFilters}>Limpar filtros</button>
          </div>
        </details>
      </div>
      <div role="tablist" aria-label="Escopo da fila" className="queue-tabs">
        {tabs.map((tab, index) => (
          <button key={tab.value} ref={(element) => { tabRefs.current[index] = element; }}
            type="button" role="tab" id={`${id}-${tab.value}`} aria-selected={props.tab === tab.value}
            aria-controls={`${id}-results`} tabIndex={props.tab === tab.value ? 0 : -1}
            onClick={() => props.onTabChange(tab.value)} onKeyDown={(event) => changeTab(event, index)}>
            {tab.label}{props.resultState === "ready" && props.counts?.[tab.value] !== undefined && <span aria-hidden="true" className="queue-tab-count">{props.counts[tab.value]}</span>}
          </button>
        ))}
      </div>
      <p className="visually-hidden">Todas mostra as conversas disponíveis no seu acesso.</p>
      <div className="queue-results" role="tabpanel" id={`${id}-results`} aria-labelledby={`${id}-${props.tab}`} tabIndex={0}>
        {props.resultState === "loading" && <p role="status" aria-busy="true">Carregando conversas.</p>}
        {props.resultState === "error" && <p role="alert">Não foi possível carregar as conversas.</p>}
        {props.resultState === "ready" && props.items.length === 0 && <p>Nenhuma conversa encontrada.</p>}
        {props.resultState === "ready" && props.items.length > 0 && (
          <ul aria-label="Conversas encontradas" className="queue-items">
            {props.items.map((item) => (
              <li key={item.id}>
                <button type="button" aria-pressed={props.selectedId === item.id} onClick={() => props.onSelect(item.id)}
                  data-wait-alert={item.waitAlert ? "true" : undefined} data-unread={item.unreadCount > 0 ? "true" : undefined}
                  className={`queue-item${item.waitAlert ? " queue-item-alert border-destructive bg-destructive/10" : ""}`}>
                  <ContactAvatar label={item.avatarLabel ?? item.contactLabel} size={40} />
                  <span className="queue-body">
                    <span className="queue-line">
                      <strong>{item.contactLabel}</strong>
                      {messageTime(item.lastMessageAt) && <time className="queue-message-time" dateTime={item.lastMessageAt} title={`Última mensagem: ${messageTime(item.lastMessageAt)}`}>{compactTime(item.lastMessageAt)}</time>}
                    </span>
                    <span className="queue-line">
                      <span className="visually-hidden">Protocolo {item.protocol}</span><span className="queue-protocol" aria-hidden="true">{item.protocol}</span>
                      {item.sectorLabel && <span className="queue-sector">{item.sectorLabel}</span>}
                      <span className="queue-flags">
                        {item.priorityLabel && <span className="queue-flag"><Flag size={12} strokeWidth={2} aria-hidden="true" /><span className="visually-hidden">Prioridade: </span>{item.priorityLabel}</span>}
                        {item.waitAlert && <span className="queue-wait" title="Passou do tempo de espera"><Clock size={14} strokeWidth={1.8} aria-hidden="true" /><span className="visually-hidden">Passou do tempo de espera.</span></span>}
                        {item.unreadCount > 0 && <span className="unread-count">{item.unreadCount}<span className="visually-hidden"> não lida(s)</span></span>}
                      </span>
                    </span>
                    {(item.statusLabel || item.assigneeLabel) && (
                      <span className="queue-line">
                        {item.statusLabel && <span className="queue-state" data-state={stateMark[item.statusLabel]}>{item.statusLabel}</span>}
                        {item.assigneeLabel && <span className="queue-assignee" title={item.assigneeLabel}>{shortLabel(item.assigneeLabel)}</span>}
                      </span>
                    )}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
