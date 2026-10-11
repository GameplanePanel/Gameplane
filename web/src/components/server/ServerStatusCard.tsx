import { useResourceClient, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { Card } from "@heroui/react";

import type { GameServer, GameTemplate } from "@/types";

import { rconAvailable } from "@/lib/capabilities";
import { formatUptime } from "@/lib/utils";

// ServerStatusCard renders the module-declared live metrics
// (spec.capabilities.status.metrics) for the Overview tab. It reads
// GET /servers/{name}/status while the server is running and lines the
// values up against the declared metrics so the layout stays stable even
// before the first reading arrives. When the game declares no metrics (or
// has no RCON) it falls back to a generic summary built from data already
// on the GameServer (phase, uptime, version, players) so the card — and
// the design's four-card right column (assets/design-export/screenshots/EZFW0.png)
// — stays populated for every template, not just ones with declared
// metrics. `gs` is optional: callers that don't pass it keep the old
// "render nothing" behavior.
export function ServerStatusCard({
  name,
  ns,
  tmpl,
  running,
  gs,
}: {
  name: string;
  ns?: string;
  tmpl?: GameTemplate;
  running: boolean;
  gs?: GameServer;
}) {
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const metrics = tmpl?.spec.capabilities?.status?.metrics ?? [];
  const show = metrics.length > 0 && rconAvailable(tmpl);

  const { data: readings } = useQuery({
    queryKey: resourceKey(resourceTarget, "server-status", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.status(name, ns),
    enabled: show && running,
    refetchInterval: 10_000,
    retry: false,
  });

  if (!show) {
    if (!gs) return null;
    const status = gs.status ?? {};
    const players =
      typeof status.agent?.playersOnline === "number" && status.agent.playersOnline >= 0
        ? String(status.agent.playersOnline)
        : "—";
    return (
      <Card className="border border-border bg-surface">
        <div className="flex items-center justify-between border-b border-border px-6 py-4">
          <h3 className="text-sm font-semibold text-foreground">Game status</h3>
          <Activity className="h-4 w-4 text-muted" />
        </div>
        <div className="px-6 py-4">
          <dl className="space-y-2 text-sm">
            <GenericStatusRow label="Phase" value={status.phase ?? "—"} />
            <GenericStatusRow label="Uptime" value={formatUptime(status.startedAt)} />
            <GenericStatusRow
              label="Version"
              value={status.agent?.gameVersion ?? "—"}
            />
            <GenericStatusRow label="Players" value={players} />
          </dl>
        </div>
      </Card>
    );
  }

  const byId = new Map((readings ?? []).map((r) => [r.id, r]));

  return (
    <Card className="border border-border bg-surface">
      <div className="flex items-center justify-between border-b border-border px-6 py-4">
        <h3 className="text-sm font-semibold text-foreground">Game status</h3>
        <Activity className="h-4 w-4 text-muted" />
      </div>
      <div className="px-6 py-4">
        <dl className="space-y-2 text-sm">
          {metrics.map((m) => {
            const reading = byId.get(m.id);
            const value = reading?.value?.trim();
            return (
              <div
                key={m.id}
                className="flex items-center justify-between gap-2 rounded-md border border-border bg-surface/60 px-3 py-2"
              >
                <dt className="text-muted">{m.displayName}</dt>
                <dd className="font-mono text-foreground">
                  {value ? (
                    <>
                      {value}
                      {m.unit ? <span className="text-muted"> {m.unit}</span> : null}
                    </>
                  ) : (
                    <span className="text-muted">{running ? "—" : "offline"}</span>
                  )}
                </dd>
              </div>
            );
          })}
        </dl>
      </div>
    </Card>
  );
}

// GenericStatusRow renders one row of the fallback (no module metrics)
// summary — same visual row shape as the declared-metric rows above.
function GenericStatusRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2 rounded-[8px] border border-border bg-surface/60 px-3 py-2">
      <dt className="text-muted">{label}</dt>
      <dd className="font-mono text-foreground">{value}</dd>
    </div>
  );
}
