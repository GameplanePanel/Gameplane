import { useResourceClient, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { Card } from "@heroui/react";

import type { GameServer, GameTemplate } from "@/types";

import { rconAvailable } from "@/lib/capabilities";
import { formatUptime } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

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
  const intl = useOptionalIntl();
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
      <Card className="border border-border bg-card p-0">
        <div className="flex items-center justify-between px-6 pt-5 pb-3">
          <h3 className="text-[11px] font-semibold uppercase tracking-[1px] text-muted">{intl.formatMessage({ id: "web.components.server.server_status_card.game_status", defaultMessage: "Game status", description: "Server status card: heading of the card that shows the game's status figures" })}</h3>
          <Activity className="h-4 w-4 text-muted" />
        </div>
        <div className="px-6 pb-5">
          <dl className="space-y-3 text-[13px]">
            <GenericStatusRow label={intl.formatMessage({ id: "web.components.server.server_status_card.phase", defaultMessage: "Phase", description: "Server status card: label of the row showing the server's current phase; a dash is shown when it is unknown" })} value={status.phase ?? "—"} />
            <GenericStatusRow label={intl.formatMessage({ id: "web.components.server.server_status_card.uptime", defaultMessage: "Uptime", description: "Server status card: label of the row showing how long the server has been running" })} value={formatUptime(status.startedAt, intl)} />
            <GenericStatusRow
              label={intl.formatMessage({ id: "web.components.server.server_status_card.version", defaultMessage: "Version", description: "Server status card: label of the row showing the installed game version; a dash is shown when it is unknown" })}
              value={status.agent?.gameVersion ?? "—"}
            />
            <GenericStatusRow label={intl.formatMessage({ id: "web.components.server.server_status_card.players", defaultMessage: "Players", description: "Server status card: label of the row showing how many players are online" })} value={players} />
          </dl>
        </div>
      </Card>
    );
  }

  const byId = new Map((readings ?? []).map((r) => [r.id, r]));

  return (
    <Card className="border border-border bg-card p-0">
      <div className="flex items-center justify-between px-6 pt-5 pb-3">
        <h3 className="text-[11px] font-semibold uppercase tracking-[1px] text-muted">{intl.formatMessage({ id: "web.components.server.server_status_card.game_status", defaultMessage: "Game status", description: "Server status card: heading of the card that shows the game's status figures" })}</h3>
        <Activity className="h-4 w-4 text-muted" />
      </div>
      <div className="px-6 pb-5">
        <dl className="space-y-3 text-[13px]">
          {metrics.map((m) => {
            const reading = byId.get(m.id);
            const value = reading?.value?.trim();
            return (
              <div
                key={m.id}
                className="flex items-center justify-between gap-2 rounded-[8px] border border-border bg-[var(--default)] px-3.5 py-2.5"
              >
                <dt className="text-muted">{m.displayName}</dt>
                <dd className="font-mono text-foreground">
                  {value ? (
                    <>
                      {value}
                      {m.unit ? <span className="text-muted"> {m.unit}</span> : null}
                    </>
                  ) : (
                    <span className="text-muted">{running ? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" }) : intl.formatMessage({ id: "web.components.server.server_status_card.offline", defaultMessage: "offline", description: "Server status card: state shown when the server is not running" })}</span>
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
    <div className="flex items-center justify-between gap-2 rounded-[8px] border border-border bg-[var(--default)] px-3.5 py-2.5">
      <dt className="text-muted">{label}</dt>
      <dd className="font-mono text-foreground">{value}</dd>
    </div>
  );
}
