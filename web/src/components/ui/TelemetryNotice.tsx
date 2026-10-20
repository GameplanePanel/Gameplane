import { useEffect, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@heroui/react";
import { Info } from "lucide-react";
import { Telemetry, type TelemetryNotice as TelemetryNoticeData, type TelemetryNoticeAction } from "@/lib/api";
import { TELEMETRY_STATEMENT_URL } from "@/lib/links";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface TelemetryNoticeProps {
  /** Navigates to /admin?section=telemetry. */
  onOpenSettings: () => void;
}

const destinationMessages = defineMessages({
  default: { id: "web.components.ui.telemetry_notice.reports_are_sent_to_default", defaultMessage: "Reports are sent to <b>{host}</b>, the Gameplane project's telemetry service.", description: "Telemetry notice: sentence naming the default telemetry receiver; {host} is its host name, shown in bold inside <b>" },
  bundled: { id: "web.components.ui.telemetry_notice.reports_are_sent_to_bundled", defaultMessage: "Reports are sent to <b>{host}</b>, the receiver bundled with this install. Reports stay inside your cluster.", description: "Telemetry notice: sentence naming the receiver bundled with the install; {host} is its host name, shown in bold inside <b>" },
  custom: { id: "web.components.ui.telemetry_notice.reports_are_sent_to_custom", defaultMessage: "Reports are sent to <b>{host}</b>, the destination your operator configured.", description: "Telemetry notice: sentence naming a telemetry destination the operator configured; {host} is its host name, shown in bold inside <b>" },
});

function destinationText(intl: IntlShape, kind: string, host: string): ReactNode {
  const message: MessageDescriptor = kind === "default"
    ? destinationMessages.default
    : kind === "bundled"
      ? destinationMessages.bundled
      : destinationMessages.custom;
  return intl.formatMessage(message, {
    host,
    b: (chunks) => <span className="font-semibold text-accent-soft-foreground">{chunks}</span>,
  });
}

const linkClass =
  "text-xs font-semibold text-accent-soft-foreground underline-offset-2 hover:underline";

/**
 * First-login telemetry notice (spec 022 FR-004, design frame lh8p8). Mount
 * only for users holding config:manage. Renders while the notice is pending;
 * posts "seen" once on mount; every action acks, then hides the banner.
 */
export function TelemetryNotice({ onOpenSettings }: TelemetryNoticeProps) {
  const intl = useOptionalIntl();
  const queryClient = useQueryClient();
  const [failed, setFailed] = useState(false);
  const seenPosted = useRef(false);
  const { data } = useQuery({
    queryKey: ["telemetry", "notice"],
    queryFn: () => Telemetry.notice(),
  });
  const pending = data?.pending === true;

  useEffect(() => {
    if (!pending || seenPosted.current) return;
    seenPosted.current = true;
    // Best effort: a failed "seen" only delays the schedule opening.
    void Telemetry.ack("seen").catch(() => {});
  }, [pending]);

  const ack = useMutation({
    mutationFn: (action: TelemetryNoticeAction) => Telemetry.ack(action),
    onMutate: () => setFailed(false),
    onSuccess: async () => {
      queryClient.setQueryData<TelemetryNoticeData>(["telemetry", "notice"], { pending: false });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["telemetry"] }),
        queryClient.invalidateQueries({ queryKey: ["admin-config"] }),
      ]);
    },
    onError: () => setFailed(true),
  });

  if (!pending) return null;
  const dest = data?.destination;

  return (
    <div className="px-4 pt-4 sm:px-6">
      <section
        aria-label={intl.formatMessage({ id: "web.components.ui.telemetry_notice.telemetry_notice", defaultMessage: "Telemetry notice", description: "Telemetry notice: screen-reader name of the notice about anonymous usage metrics; not visible on screen" })}
        className="flex w-full gap-3 rounded-lg border border-accent-soft-foreground bg-accent-soft p-4"
      >
        <Info aria-hidden="true" className="h-5 w-5 shrink-0 text-accent-soft-foreground" />
        <div className="flex min-w-0 flex-1 flex-col gap-3">
          <p className="text-sm font-semibold text-accent-soft-foreground">{intl.formatMessage({ id: "web.components.ui.telemetry_notice.anonymous_usage_metrics_are_on_for_this", defaultMessage: "Anonymous usage metrics are on for this install.", description: "Telemetry notice: bold heading saying anonymous usage metrics are on for this installation" })}</p>
          {dest?.host ? (
            <p className="text-[13px] text-foreground">{destinationText(intl, dest.kind, dest.host)}</p>
          ) : null}
          <div className="flex flex-col gap-4 sm:flex-row sm:gap-6">
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              <p className="text-[13px] font-semibold text-foreground">{intl.formatMessage({ id: "web.components.ui.telemetry_notice.basic_metrics", defaultMessage: "Basic metrics", description: "Telemetry notice: heading of the first group of shared data, the basic metrics" })}</p>
              <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.ui.telemetry_notice.gameplane_version_number_of_servers", defaultMessage: "Gameplane version, number of servers, number of templates.", description: "Telemetry notice: description of the basic metrics, listing the Gameplane version, number of servers and number of templates" })}</p>
            </div>
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              <p className="text-[13px] font-semibold text-foreground">{intl.formatMessage({ id: "web.components.ui.telemetry_notice.extended_metrics", defaultMessage: "Extended metrics", description: "Telemetry notice: heading of the second group of shared data, the extended metrics" })}</p>
              <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.ui.telemetry_notice.kubernetes_version_distribution_cpu", defaultMessage: "Kubernetes version, distribution, CPU architecture and node-count band; servers per official game module (others counted as custom); which optional features are in use; and a random install ID.", description: "Telemetry notice: description of the extended metrics, listing the Kubernetes version, distribution, CPU architecture, node-count band, servers per game module, optional features and an install ID" })}</p>
            </div>
          </div>
          {failed && (
            <p role="alert" className="text-xs text-danger">{intl.formatMessage({ id: "web.components.ui.telemetry_notice.could_not_save_your_choice_try_again", defaultMessage: "Could not save your choice. Try again.", description: "Telemetry notice: error message shown when saving the telemetry choice fails; it asks the user to try again" })}</p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" size="sm" isDisabled={ack.isPending} onPress={() => ack.mutate("keep")}>{intl.formatMessage({ id: "web.components.ui.telemetry_notice.keep_sharing", defaultMessage: "Keep sharing", description: "Telemetry notice: button that keeps the current telemetry sharing on; keep it short" })}</Button>
            <Button variant="outline" size="sm" isDisabled={ack.isPending} onPress={() => ack.mutate("extended-off")}>{intl.formatMessage({ id: "web.components.ui.telemetry_notice.turn_off_extended", defaultMessage: "Turn off extended", description: "Telemetry notice: button that turns off the extended metrics only; keep it short" })}</Button>
            <Button variant="outline" size="sm" isDisabled={ack.isPending} onPress={() => ack.mutate("all-off")}>{intl.formatMessage({ id: "web.components.ui.telemetry_notice.turn_off_all", defaultMessage: "Turn off all", description: "Telemetry notice: button that turns off all telemetry sharing; keep it short" })}</Button>
            <div className="ml-auto flex items-center gap-2.5">
              <button type="button" onClick={onOpenSettings} className={linkClass}>{intl.formatMessage({ id: "web.components.ui.telemetry_notice.open_telemetry_settings", defaultMessage: "Open Telemetry settings", description: "Telemetry notice: link-style button that opens the Telemetry settings; keep it short" })}</button>
              <span aria-hidden="true" className="select-none text-muted">{intl.formatMessage({ id: "web.common.separator_dot", defaultMessage: "·", description: "Visual separator between two items" })}</span>
              <a href={TELEMETRY_STATEMENT_URL} target="_blank" rel="noopener noreferrer" className={linkClass}>{intl.formatMessage({ id: "web.components.ui.telemetry_notice.data_handling_statement", defaultMessage: "Data-handling statement", description: "Telemetry notice: link to the data-handling statement, which opens in a new tab; keep it short" })}</a>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
