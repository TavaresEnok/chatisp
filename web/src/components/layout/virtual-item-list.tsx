import { useVirtualizer } from "@tanstack/react-virtual";
import { useRef } from "react";
import type { AreaItem } from "@/api/area-snapshot";

type VirtualItemListProps = {
  items: readonly AreaItem[];
};

export function VirtualItemList({ items }: VirtualItemListProps) {
  const parentRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 44,
    overscan: 6,
    initialRect: { width: 320, height: 480 },
  });

  return (
    <div
      ref={parentRef}
      className="max-h-96 overflow-auto rounded-xl ring-1 ring-foreground/10"
    >
      <ul
        aria-label="Registros da área"
        className="relative w-full"
        style={{ height: `${virtualizer.getTotalSize()}px` }}
      >
        {virtualizer.getVirtualItems().map((virtualRow) => {
          const item = items[virtualRow.index];
          if (!item) {
            return null;
          }

          return (
            <li
              key={item.id}
              className="absolute top-0 left-0 flex w-full items-center px-4 text-sm"
              style={{
                height: `${virtualRow.size}px`,
                transform: `translateY(${virtualRow.start}px)`,
              }}
            >
              {item.label}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
