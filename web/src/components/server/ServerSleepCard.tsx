import { CalendarClock, Megaphone, Moon } from "lucide-react";
import { Card } from "@heroui/react";

import type { GameServer } from "@/types";
import { formatRelative, capitalize } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

export function ServerSleepCard({ gs }: { gs?: GameServer }) {
  const intl = useOptionalIntl();
  const spec = gs?.spec.idle;
  const status = gs?.status?.idle;

  // Render nothing unless idle is enabled or there's current status. A
  // disabled-but-materialized spec (enabled: false) must not render an empty
  // card; a status left over from before the disable took effect still
  // should, so that transient window isn't invisible either.
  if (!spec?.enabled && !status) return null;

  const asleep = status?.asleep === true;
  const emptySince = status?.emptySince;
  const asleepSince = status?.asleepSince;
  const lastWakeTime = status?.lastWakeTime;
  const reason = status?.reason;
  const afterMinutes = spec?.afterMinutes ?? 30;
  const wakeWindows = spec?.wakeWindows ?? [];
  // The operator (gameserver_idle.go's idleDecide/idleEligible) is
  // authoritative for every string status.idle.reason can hold; this is the
  // only one that means the sleep trigger can never fire. Every other reason
  // — busy, wrong phase, stale heartbeat, players online, counting down, or
  // just woken — is a normal, working state and must not read as broken.
  const neverSleeps = reason === "this game reports no player count";

  return (
    <Card className="border border-border bg-card p-0">
      {/* Header */}
      <div className="flex items-center justify-between px-6 pt-5 pb-3">
        <h3 className="text-[11px] font-semibold uppercase tracking-[1px] text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.sleep", defaultMessage: "Sleep", description: "Server sleep card: heading of the card that shows the server's sleep state" })}</h3>
        <Moon className="h-4 w-4 text-muted" />
      </div>

      {/* Content */}
      <div className="space-y-3 px-6 pb-5 text-[13px]">
        {/* Asleep state */}
        {asleep && (
          <>
            <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
              <dt className="text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.state", defaultMessage: "State", description: "Server sleep card: label in the sleep details for the current sleep state, such as Asleep or Counting down" })}</dt>
              <dd className="font-mono text-fg">{intl.formatMessage({ id: "web.components.server.server_sleep_card.asleep", defaultMessage: "Asleep", description: "Server sleep card: value shown for the sleep state when the server is asleep" })}</dd>
            </div>
            {asleepSince && (
              <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
                <dt className="text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.asleep_since", defaultMessage: "Asleep since", description: "Server sleep card: label in the sleep details for when the server fell asleep, shown in relative time" })}</dt>
                <dd className="font-mono text-fg">{formatRelative(asleepSince, intl)}</dd>
              </div>
            )}
            {lastWakeTime && (
              <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
                <dt className="text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.last_woken", defaultMessage: "Last woken", description: "Server sleep card: label in the sleep details for when the server was last woken, shown in relative time" })}</dt>
                <dd className="font-mono text-fg">{formatRelative(lastWakeTime, intl)}</dd>
              </div>
            )}
            {/* A wake window that fails to parse keeps the server asleep
                forever with no other signal — the operator deliberately
                doesn't fail the reconcile for it, so this is the only place
                that surfaces it. */}
            {reason && reason !== "asleep (no players)" && (
              <div className="rounded-md bg-surface/40 px-3 py-2 text-xs text-warning">
                {capitalize(reason)}
              </div>
            )}
            <div className="rounded-md bg-surface/40 px-3 py-2 text-xs text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.waking_takes_normal_boot_time_before", defaultMessage: "Waking takes normal boot time before players can connect.", description: "Server sleep card: note in the countdown state saying that waking takes normal boot time before players can connect" })}</div>
          </>
        )}

        {/* Counting down state */}
        {!asleep && emptySince && (
          <>
            <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
              <dt className="text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.state", defaultMessage: "State", description: "Server sleep card: label in the sleep details for the current sleep state, such as Asleep or Counting down" })}</dt>
              <dd className="font-mono text-fg">{reason ? capitalize(reason) : intl.formatMessage({ id: "web.components.server.server_sleep_card.counting_down", defaultMessage: "Counting down", description: "Server sleep card: status text while the sleep countdown runs" })}</dd>
            </div>
            <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
              <dt className="text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.empty_since", defaultMessage: "Empty since", description: "Server sleep card: label in the sleep details for when the server became empty of players, shown in relative time" })}</dt>
              <dd className="font-mono text-fg">{formatRelative(emptySince, intl)}</dd>
            </div>
          </>
        )}

        {/* Active, not counting down: the operator's reason string IS the
            state (players online, stale heartbeat, wrong phase, busy, just
            woken, …) — only the one no-player-count reason means the
            trigger can never fire, so that's the only case worth a distinct
            label and an explanatory sub-line. */}
        {!asleep && !emptySince && reason && (
          <>
            {neverSleeps ? (
              <>
                <div className="flex items-start gap-4 rounded-[8px] border border-warning-soft-foreground bg-warning-soft px-4 py-3">
                  <Megaphone className="mt-0.5 h-4 w-4 shrink-0 text-warning-soft-foreground" />
                  <div className="min-w-0 flex-1 space-y-0.5 text-warning-soft-foreground">
                    <div className="text-[14px] font-medium">{intl.formatMessage({ id: "web.components.server.server_sleep_card.will_never_sleep", defaultMessage: "Will never sleep", description: "Server sleep card: heading of the warning shown when the server will never go to sleep; the reason is shown in the text below" })}</div>
                    <div className="text-[13px]">{intl.formatMessage({ id: "web.components.server.server_sleep_card.reason_sentence", defaultMessage: "{reason}.", description: "Server sleep card: sleep reason as a sentence; {reason} is the capitalized reason" }, { reason: capitalize(reason) })}</div>
                  </div>
                </div>
                <p className="text-xs leading-[1.4] text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.idle_auto_sleep_needs_a_live_player", defaultMessage: "Idle auto-sleep needs a live player count. This game exposes none over RCON or a query protocol, so the countdown never starts.", description: "Server sleep card: note under the never-sleep warning; idle auto-sleep needs a live player count this game cannot give, so the countdown never starts" })}</p>
              </>
            ) : (
              <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
                <dt className="text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.state", defaultMessage: "State", description: "Server sleep card: label in the sleep details for the current sleep state, such as Asleep or Counting down" })}</dt>
                <dd className="font-mono text-fg">{capitalize(reason)}</dd>
              </div>
            )}
          </>
        )}

        {/* Just-enabled, not yet reconciled: none of the three blocks above
            apply, but staying silent here reads as the change not having
            taken effect. */}
        {spec?.enabled && !asleep && !emptySince && !reason && (
          <div className="rounded-md border border-border bg-surface/60 px-3 py-2">
            <span className="font-mono text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.waiting_for_the_operator", defaultMessage: "Waiting for the operator", description: "Server sleep card: status text shown after sleep is enabled, until the operator has applied the change" })}</span>
          </div>
        )}

        {/* Configuration */}
        {spec?.enabled && (
          <>
            <div className="pt-1">
              <div className="text-[11px] font-semibold uppercase tracking-[1px] text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.configured", defaultMessage: "Configured", description: "Server sleep card: small heading above the configured sleep settings" })}</div>
            </div>

            <div className="flex items-center justify-between rounded-[8px] border border-border bg-[var(--default)] px-3.5 py-2.5">
              <dt className="text-[13px] text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.sleep_after", defaultMessage: "Sleep after", description: "Server sleep card: label for the idle time before the server sleeps; the value beside it is the configured number of minutes" })}</dt>
              <dd className="font-mono text-[13px] text-foreground">{intl.formatMessage({ id: "web.components.server.server_sleep_card.afterminutes_minute", defaultMessage: "{afterMinutes, plural, one {{minutes} minute} other {{minutes} minutes}}", description: "Server sleep card: minutes until the server sleeps; {afterMinutes} is the number of minutes (selects the plural form) and {minutes} is the same number shown as digits without grouping" }, { afterMinutes, minutes: String(afterMinutes) })}
              </dd>
            </div>

            <div className="space-y-3">
              {wakeWindows.length > 0 ? (
                wakeWindows.map((cron, i) => (
                  <div
                    key={i}
                    className="flex items-center justify-between rounded-[8px] border border-border bg-[var(--default)] px-3.5 py-2.5"
                  >
                    <CalendarClock className="h-3.5 w-3.5 text-muted" />
                    <span className="font-mono text-[13px] text-foreground">{cron}</span>
                  </div>
                ))
              ) : (
                <dd className="text-xs text-muted">{intl.formatMessage({ id: "web.components.server.server_sleep_card.not_configured", defaultMessage: "Not configured", description: "Server sleep card: value shown in the scheduled sleep line when no cron schedule is set" })}</dd>
              )}
            </div>
          </>
        )}
      </div>
    </Card>
  );
}
