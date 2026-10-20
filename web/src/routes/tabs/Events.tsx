import { useResourceClient, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useState, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Card } from "@heroui/react";
import type { GameServer } from "@/types";

import { EventList } from "@/components/server/EventList";
import { mapServerEvent, type NormalizedServerEvent } from "@/lib/events";
import { useOptionalIntl } from "@/lib/i18n/format";

type FilterType = "all" | "info" | "warnings";

export function EventsTab({
  name,
  ns,
  gs,
}: {
  name: string;
  ns?: string;
  gs?: GameServer;
}) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const [filter, setFilter] = useState<FilterType>("all");

  const { data: rawEvents } = useQuery({
    queryKey: resourceKey(resourceTarget, "events", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.events(name, ns),
    enabled: !!name,
    refetchInterval: gs?.status?.phase === "Running" ? 30_000 : 5_000,
    retry: false,
  });

  // ⚡ Bolt: Memoize raw events mapping to prevent unnecessary array recreation
  // Expected impact: Eliminates O(N) mapping on every render cycle when polling
  const events: NormalizedServerEvent[] = useMemo(() => {
    return (Array.isArray(rawEvents) ? rawEvents : []).map(mapServerEvent);
  }, [rawEvents]);

  // ⚡ Bolt: Memoize filtered events to prevent unnecessary array recreation
  // Expected impact: Eliminates O(N) filtering when only filter changes or on general renders
  const filteredEvents = useMemo(() => {
    return events.filter((e) => {
      if (filter === "all") return true;
      if (filter === "info") return e.kind === "info";
      if (filter === "warnings") return e.kind === "warn" || e.kind === "error";
      return true;
    });
  }, [events, filter]);

  return (
    <div className="space-y-6 p-6">
      <Card className="border border-border bg-surface">
        <Card.Header className="flex flex-col gap-4">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-semibold text-foreground">{intl.formatMessage({ id: "web.routes.tabs.events.events", defaultMessage: "Events", description: "Events tab: heading of the events card that lists the server's events, with filter buttons beside it" })}</h2>
            <div className="flex gap-1">
              {["all", "info", "warnings"].map((f) => {
                const filterValue = f as FilterType;
                const label =
                  filterValue === "all"
                    ? intl.formatMessage({ id: "web.routes.tabs.events.filter_all", defaultMessage: "All", description: "Events: filter button showing every event" })
                    : filterValue === "info"
                      ? intl.formatMessage({ id: "web.routes.tabs.events.filter_info", defaultMessage: "Info", description: "Events: filter button showing info events only" })
                      : intl.formatMessage({ id: "web.routes.tabs.events.filter_warnings", defaultMessage: "Warnings", description: "Events: filter button showing warning and error events only" });
                return (
                  <Button
                    key={filterValue}
                    size="sm"
                    variant={filter === filterValue ? "primary" : "ghost"}
                    onClick={() => setFilter(filterValue)}
                    className="h-7 rounded-full px-3 text-xs font-medium"
                  >
                    {label}
                  </Button>
                );
              })}
            </div>
          </div>
        </Card.Header>
        <Card.Content className="px-0 py-0">
          <EventList
            events={filteredEvents}
            emptyMessage={
              filter === "all"
                ? intl.formatMessage({ id: "web.routes.tabs.events.empty_all", defaultMessage: "No events yet.", description: "Events: shown when the server has no events" })
                : filter === "info"
                  ? intl.formatMessage({ id: "web.routes.tabs.events.empty_info", defaultMessage: "No info events.", description: "Events: shown when there are no info events" })
                  : intl.formatMessage({ id: "web.routes.tabs.events.empty_warnings", defaultMessage: "No warnings or errors.", description: "Events: shown when there are no warning or error events" })
            }
          />
        </Card.Content>
      </Card>
    </div>
  );
}
