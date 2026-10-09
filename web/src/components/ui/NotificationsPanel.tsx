import { useState, useEffect } from "react";
import type { JSX } from "react";
import { useQueryClient, type QueryFilters } from "@tanstack/react-query";
import {
  Popover,
  Card,
} from "@heroui/react";
import { Bell } from "lucide-react";
import { openEventStream, queryFilterForKind, type GameplaneEvent } from "@/lib/sse";

interface Notice {
  id: number;
  text: string;
  at: string;
}

// Window for coalescing SSE-driven cache invalidations per query key.
const INVALIDATE_COALESCE_MS = 500;

// The events feed is local-only. Standalone installations keep the bell
// available without repeatedly opening a stream against a missing cluster.
export interface NotificationsPanelProps { enabled?: boolean }

// phaseOf reads status.phase off a watch object ("" when absent).
function phaseOf(obj: GameplaneEvent["object"] | undefined): string {
  const status = obj?.status as { phase?: unknown } | undefined;
  return typeof status?.phase === "string" ? status.phase : "";
}

/**
 * NotificationsPanel opens the /events SSE stream: each watch event
 * invalidates the matching TanStack Query cache (so views refresh without
 * waiting for the next poll) and is buffered into a dropdown panel. The
 * badge shows the unread count.
 */
export function NotificationsPanel({ enabled = true }: NotificationsPanelProps): JSX.Element {
  const qc = useQueryClient();
  const [notices, setNotices] = useState<Notice[]>([]);
  const [open, setOpen] = useState(false);
  const [unread, setUnread] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    let seq = 0;
    // Coalesce invalidations per query key: a burst of watch events for one
    // kind costs a single refetch. An in-flight fetch is never cancelled (a
    // steady event stream would starve it), but it may predate the event, so
    // the invalidation waits for it to settle and then refetches.
    const pending = new Map<string, ReturnType<typeof setTimeout>>();
    const flush = (id: string, filters: QueryFilters) => {
      if (qc.isFetching(filters) > 0) {
        pending.set(id, setTimeout(() => flush(id, filters), INVALIDATE_COALESCE_MS));
        return;
      }
      pending.delete(id);
      void qc.invalidateQueries(filters, { cancelRefetch: false });
    };
    // Agent heartbeats patch status.agent every ~20 s per server, which
    // arrives as MODIFIED with no spec or phase change. Remember each
    // object's (generation, phase) signature and drop a MODIFIED that leaves
    // it unchanged: it neither notifies nor invalidates (the pollers cover
    // heartbeat-only fields). ADDED and DELETED always pass.
    const seen = new Map<string, string>();
    const isMeaningful = (ev: GameplaneEvent): boolean => {
      const meta = ev.object?.metadata;
      const key = `${ev.kind}/${meta?.namespace ?? ""}/${meta?.name ?? ""}`;
      if (ev.eventType === "DELETED") {
        seen.delete(key);
        return true;
      }
      const sig = `${meta?.generation ?? ""}/${phaseOf(ev.object)}`;
      const prev = seen.get(key);
      seen.set(key, sig);
      return ev.eventType !== "MODIFIED" || prev !== sig;
    };
    const dispose = openEventStream({
      onEvent: (ev: GameplaneEvent) => {
        if (!isMeaningful(ev)) return;
        const filters = queryFilterForKind(ev.kind);
        if (filters) {
          const id = ev.kind;
          if (!pending.has(id)) {
            pending.set(id, setTimeout(() => flush(id, filters), INVALIDATE_COALESCE_MS));
          }
        }
        const name = ev.object?.metadata?.name ?? "";
        const verb = ev.eventType.toLowerCase();
        seq += 1;
        const notice: Notice = {
          id: seq,
          text: `${verb} ${ev.kind.replace(/s$/, "")} ${name}`.trim(),
          at: new Date().toLocaleTimeString(),
        };
        setNotices((prev) => [notice, ...prev].slice(0, 50));
        setUnread((u) => u + 1);
      },
    });
    return () => {
      for (const timer of pending.values()) clearTimeout(timer);
      pending.clear();
      dispose();
    };
  }, [qc, enabled]);

  return (
    <Popover
      isOpen={open}
      onOpenChange={(newOpen) => {
        setOpen(newOpen);
        if (newOpen) {
          setUnread(0);
        }
      }}
    >
      <Popover.Trigger>
        <div
          aria-label="Notifications"
          className="inline-flex items-center justify-center w-10 h-10 rounded-lg hover:bg-default-100 cursor-pointer transition-colors relative"
        >
          <Bell className="h-[18px] w-[18px]" />
          {unread > 0 && (
            <span className="absolute right-1 top-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-primary px-1 text-[9px] font-medium text-primary-fg">
              {unread > 9 ? "9+" : unread}
            </span>
          )}
        </div>
      </Popover.Trigger>
      <Popover.Content className="w-72 p-0">
        <Card className="border-none shadow-lg">
          <div className="border-b border-divider px-3 py-2 text-xs font-medium text-default-500">
            Recent activity
          </div>
          {notices.length === 0 ? (
            <div className="px-3 py-4 text-sm text-default-500">
              No recent activity.
            </div>
          ) : (
            <ul className="max-h-80 overflow-auto">
              {notices.map((n) => (
                <li
                  key={n.id}
                  className="flex items-center justify-between gap-2 px-3 py-2 text-sm border-b border-divider last:border-b-0"
                >
                  <span className="truncate font-mono text-xs text-default-700">
                    {n.text}
                  </span>
                  <span className="shrink-0 text-[10px] text-default-500">
                    {n.at}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </Popover.Content>
    </Popover>
  );
}
