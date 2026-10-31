/** Presentation models; these are not HTTP DTOs. */
export type QueueTab = "mine" | "unassigned" | "all";
export type QueueOrder = "wait" | "lastMessage";
export type QueueOption = { value: string; label: string };

export type QueueFilters = {
  status: string;
  sector: string;
  assignee: string;
  inbox: string;
  tag: string;
  from: string;
  to: string;
};

export type QueueFilterOptions = {
  status: readonly QueueOption[];
  sector: readonly QueueOption[];
  assignee: readonly QueueOption[];
  inbox: readonly QueueOption[];
  tag: readonly QueueOption[];
};

export type QueueItem = {
  id: string;
  contactLabel: string;
  protocol: string;
  priorityLabel: string | null;
  unreadCount: number;
};

export type QueueResultState = "loading" | "error" | "ready";
