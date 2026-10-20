import React from "react";
import type { NormalizedServerEvent } from "@/lib/events";
import { formatRelative } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

// ⚡ Bolt: Memoize EventDot to prevent unnecessary re-renders of list items.
// Expected impact: Prevents React from diffing the EventDot component tree when
// its kind prop hasn't changed.
const EventDot = React.memo(function EventDot({ kind }: { kind: NormalizedServerEvent["kind"] }) {
  const color = {
    info: "bg-accent",
    warn: "bg-warning",
    error: "bg-danger",
  }[kind];
  return <span className={`mt-1.5 inline-block h-2 w-2 rounded-full ${color}`} />;
});

// ⚡ Bolt: Memoize EventList to prevent entire list re-rendering when parent polls.
// Expected impact: Events Tab and Overview Tab poll for events every 5-30s. This
// optimization completely skips rendering the often 50+ item long event list when
// the API returns unchanged events.
export const EventList = React.memo(function EventList({
  events,
  emptyMessage,
}: {
  events: NormalizedServerEvent[];
  emptyMessage?: string;
}) {
  const intl = useOptionalIntl();
  const emptyText = emptyMessage ?? intl.formatMessage({ id: "web.components.server.event_list.no_events_yet", defaultMessage: "No events yet.", description: "Event list: text shown when there are no events, used when no emptyMessage is passed" });
  return (
    <>
      {events.length === 0 && (
        <div className="px-6 pb-6 text-sm text-muted">
          {emptyText}
        </div>
      )}
      <ul className="divide-y divide-border">
        {events.map((e) => (
          <li key={e.id} className="flex items-start gap-3 px-6 py-3">
            <EventDot kind={e.kind} />
            <div className="min-w-0 flex-1">
              <div className="text-sm text-foreground">{e.message}</div>
              <div className="pt-0.5 text-xs text-muted">
                {e.source ?? intl.formatMessage({ id: "web.components.server.event_list.system_source", defaultMessage: "system", description: "Event list: source shown when an event has no source" })}
              </div>
            </div>
            <div className="text-xs text-muted">{formatRelative(e.ts, intl)}</div>
          </li>
        ))}
      </ul>
    </>
  );
});
