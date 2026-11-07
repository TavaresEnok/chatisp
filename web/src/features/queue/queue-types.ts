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
  statusLabel?: string;
  assigneeLabel?: string;
  sectorLabel?: string;
  lastMessageAt?: string;
  id: string;
  contactLabel: string;
  avatarLabel?: string;
  protocol: string;
  priorityLabel: string | null;
  unreadCount: number;
  waitAlert?: boolean;
};

export type QueueResultState = "loading" | "error" | "ready";
