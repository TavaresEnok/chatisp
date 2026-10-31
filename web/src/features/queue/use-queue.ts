import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchQueue } from "@/api/queue";
import type { PanelPrincipal } from "@/stores/session-store";
import type { QueueFilters, QueueOrder, QueueTab } from "./queue-types";

const empty: QueueFilters = { status: "", sector: "", assignee: "", inbox: "", tag: "", from: "", to: "" };

export function useQueue(principal: PanelPrincipal, inbox: string | null, selectInbox?: (id: string) => void) {
  const [tab, setTab] = useState<QueueTab>("mine");
  const [order, setOrder] = useState<QueueOrder>("wait");
  const [search, setSearch] = useState("");
  const [filters, setFilters] = useState<QueueFilters>(empty);
  const [page, setPage] = useState<{ inbox: string | null; cursor: string }>({ inbox, cursor: "" });
  const cursor = page.inbox === inbox ? page.cursor : "";
  const setCursor = (value: string) => setPage({ inbox, cursor: value });
  const scope = [principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId, inbox];
  const query = { tab, order, ...filters, inbox: inbox ?? "", search, page_cursor: cursor, limit: 50 };
  const result = useQuery({ queryKey: ["m3b-queue", ...scope, query], queryFn: ({ signal }) => fetchQueue(query, signal), enabled: !!inbox, retry: false });
  return { tab, setTab: (value: QueueTab) => { setCursor(""); setTab(value); }, order, setOrder: (value: QueueOrder) => { setCursor(""); setOrder(value); }, search, setSearch: (value: string) => { setCursor(""); setSearch(value); }, filters, setFilters: (value: QueueFilters) => { setCursor(""); if (value.inbox && value.inbox !== inbox) selectInbox?.(value.inbox); setFilters(value); }, cursor, setCursor, result };
}
