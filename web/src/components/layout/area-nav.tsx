import {
  ChartColumn,
  Contact,
  LayoutDashboard,
  MessagesSquare,
  Settings,
  Users,
  type LucideIcon,
} from "lucide-react";
import type { KeyboardEvent } from "react";
import { NavLink } from "react-router";
import { areas, type AreaId } from "@/lib/areas";
import { cn } from "@/lib/utils";

const areaIcons: Record<AreaId, LucideIcon> = {
  dashboard: LayoutDashboard,
  conversations: MessagesSquare,
  contacts: Contact,
  teams: Users,
  reports: ChartColumn,
  settings: Settings,
};

function focusOffset(index: number, length: number, key: string): number | null {
  if (key === "ArrowDown" || key === "ArrowRight") {
    return (index + 1) % length;
  }
  if (key === "ArrowUp" || key === "ArrowLeft") {
    return (index - 1 + length) % length;
  }
  if (key === "Home") {
    return 0;
  }
  if (key === "End") {
    return length - 1;
  }
  return null;
}

function moveFocus(current: HTMLElement, key: string) {
  const nav = current.closest("nav");
  if (!nav) {
    return;
  }

  const links = Array.from(nav.querySelectorAll<HTMLAnchorElement>("a[href]"));
  const index = links.findIndex((link) => link === current);
  if (index < 0 || links.length === 0) {
    return;
  }

  const nextIndex = focusOffset(index, links.length, key);
  if (nextIndex === null) {
    return;
  }

  links[nextIndex]?.focus();
}

function onNavigationKeyDown(event: KeyboardEvent<HTMLElement>) {
  const key = event.key;
  const moves =
    key === "ArrowDown" ||
    key === "ArrowUp" ||
    key === "ArrowLeft" ||
    key === "ArrowRight" ||
    key === "Home" ||
    key === "End";
  if (!moves || !(event.target instanceof HTMLElement)) {
    return;
  }

  event.preventDefault();
  moveFocus(event.target, key);
}

export function AreaNav() {
  return (
    <nav aria-label="Áreas do painel" onKeyDown={onNavigationKeyDown}>
      <ul className="flex gap-1 overflow-x-auto p-2 md:flex-col">
        {areas.map((area) => {
          const Icon = areaIcons[area.id];
          return (
            <li key={area.id} className="shrink-0 md:shrink">
              <NavLink
                to={area.path}
                end
                className={({ isActive }) =>
                  cn(
                    "flex min-h-11 items-center gap-2 rounded-lg px-3 text-sm font-medium text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground",
                    isActive
                      ? "bg-primary text-primary-foreground"
                      : "hover:bg-muted",
                  )
                }
              >
                <Icon aria-hidden="true" className="size-4 shrink-0" />
                {area.label}
              </NavLink>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
