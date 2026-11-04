import { useId, useRef } from "react";
import type { KeyboardEvent } from "react";
import { QueueSearch } from "./queue-search";
import type { QueueFilterOptions, QueueFilters, QueueItem, QueueOrder, QueueResultState, QueueTab } from "./queue-types";

export type QueueControlsProps = {
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

export function QueueControls(props: QueueControlsProps) {
  const id = useId();
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
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
  return (
    <section aria-label="Fila de conversas" className="flex min-w-0 flex-col gap-4">
      <div role="tablist" aria-label="Escopo da fila" className="flex flex-wrap gap-2">
        {tabs.map((tab, index) => (
          <button key={tab.value} ref={(element) => { tabRefs.current[index] = element; }}
            type="button" role="tab" id={`${id}-${tab.value}`} aria-selected={props.tab === tab.value}
            aria-controls={`${id}-results`} tabIndex={props.tab === tab.value ? 0 : -1}
            className="min-h-11 rounded-lg border px-3 aria-selected:bg-muted focus-visible:outline-2"
            onClick={() => props.onTabChange(tab.value)} onKeyDown={(event) => changeTab(event, index)}>
            {tab.label}
          </button>
        ))}
      </div>
      <p className="text-sm text-muted-foreground">Todas mostra as conversas disponíveis no seu acesso.</p>
      <QueueSearch value={props.search} onChange={props.onSearchChange} />
      <fieldset className="flex flex-wrap gap-3">
        <legend className="font-medium">Filtros</legend>
        {fields.map(({ key, label }) => (
          <label key={key} className="flex flex-col gap-1">{label}
            <select className="min-h-11 rounded-lg border border-input px-2" value={props.filters[key]}
              onChange={(event) => setFilter(key, event.target.value)}>
              <option value="">Todos</option>
              {props.filterOptions[key].map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
        ))}
        <label className="flex flex-col gap-1">De
          <input type="date" className="min-h-11 rounded-lg border border-input px-2" value={props.filters.from} onChange={(event) => setFilter("from", event.target.value)} />
        </label>
        <label className="flex flex-col gap-1">Até
          <input type="date" className="min-h-11 rounded-lg border border-input px-2" value={props.filters.to} onChange={(event) => setFilter("to", event.target.value)} />
        </label>
        <button type="button" className="min-h-11 self-end rounded-lg border px-3" onClick={clearFilters}>Limpar filtros</button>
      </fieldset>
      <label className="flex flex-col gap-1">Ordenar por
        <select className="min-h-11 rounded-lg border border-input px-2" value={props.order}
          onChange={(event) => props.onOrderChange(event.target.value as QueueOrder)}>
          <option value="wait">Tempo de espera</option>
          <option value="lastMessage">Última mensagem</option>
        </select>
      </label>
      <div role="tabpanel" id={`${id}-results`} aria-labelledby={`${id}-${props.tab}`} tabIndex={0}>
        {props.resultState === "loading" && <p role="status" aria-busy="true">Carregando conversas.</p>}
        {props.resultState === "error" && <p role="alert">Não foi possível carregar as conversas.</p>}
        {props.resultState === "ready" && props.items.length === 0 && <p>Nenhuma conversa encontrada.</p>}
        {props.resultState === "ready" && props.items.length > 0 && (
          <ul aria-label="Conversas encontradas" className="flex flex-col gap-2">
            {props.items.map((item) => (
              <li key={item.id}>
                <button type="button" aria-pressed={props.selectedId === item.id} onClick={() => props.onSelect(item.id)}
                  data-wait-alert={item.waitAlert ? "true" : undefined}
                  className={`flex min-h-11 w-full flex-wrap items-center gap-2 rounded-lg border p-2 text-left focus-visible:outline-2${item.waitAlert ? " border-destructive bg-destructive/10" : ""}`}>
                  <span>{item.contactLabel}</span><span>Protocolo {item.protocol}</span>
                  {item.priorityLabel && <span>Prioridade: {item.priorityLabel}</span>}
                  {item.unreadCount > 0 && <span>{item.unreadCount} não lida(s)</span>}
                  {item.waitAlert && <span>Passou do tempo de espera.</span>}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
