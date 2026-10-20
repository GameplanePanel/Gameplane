import { useResourceAccess, useResourcePermissions, resourceCan } from "@/lib/resourceTarget";
import { useFormat } from "@/lib/i18n/format";
import { useEffect, useState } from "react";
import { defineMessages, type MessageDescriptor } from "react-intl";
import {
  Input,
  Select,
  ListBox,
  ListBoxItem,
  Label,
  Description,
  FieldError,
  TextField,
  Switch,
} from "@heroui/react";
import type { CaptureConfiguration } from "@/types";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";

// Cluster ceiling on the per-server retention override (operator/api/v1alpha1
// CaptureConfiguration.RetentionSeconds: kubebuilder Minimum=1/Maximum=604800,
// default 86400). 7 days is a storage-limitation-informed engineering default
// (GDPR Art. 5(1)(e) informed the choice) — NOT a legal requirement, and the
// UI must not claim otherwise.
const CLUSTER_MAX_RETENTION_SECONDS = 604800;
const DEFAULT_RETENTION_SECONDS = 86400;

type RetentionUnit = "seconds" | "minutes" | "hours" | "days";

const UNIT_SECONDS: Record<RetentionUnit, number> = {
  seconds: 1,
  minutes: 60,
  hours: 3600,
  days: 86400,
};

const UNIT_LABELS = defineMessages({
  seconds: { id: "web.routes.tabs.settings.network_capture.unit_seconds", defaultMessage: "seconds", description: "Network capture: retention window unit option, seconds" },
  minutes: { id: "web.routes.tabs.settings.network_capture.unit_minutes", defaultMessage: "minutes", description: "Network capture: retention window unit option, minutes" },
  hours: { id: "web.routes.tabs.settings.network_capture.unit_hours", defaultMessage: "hours", description: "Network capture: retention window unit option, hours" },
  days: { id: "web.routes.tabs.settings.network_capture.unit_days", defaultMessage: "days", description: "Network capture: retention window unit option, days" },
});

const UNIT_OPTIONS: { value: RetentionUnit; label: MessageDescriptor }[] = [
  { value: "seconds", label: UNIT_LABELS.seconds },
  { value: "minutes", label: UNIT_LABELS.minutes },
  { value: "hours", label: UNIT_LABELS.hours },
  { value: "days", label: UNIT_LABELS.days },
];

// Picks the coarsest unit that displays the given second count as a whole
// number, so "86400" reads back as "1 day" instead of "86400 seconds".
function bestUnit(seconds: number): RetentionUnit {
  if (seconds > 0 && seconds % 86400 === 0) return "days";
  if (seconds > 0 && seconds % 3600 === 0) return "hours";
  if (seconds > 0 && seconds % 60 === 0) return "minutes";
  return "seconds";
}

export function NetworkCaptureSection({ draft, onChange, onValidityChange }: SectionProps) {
  const intl = useOptionalIntl();
  const fmt = useFormat();
  const access = useResourceAccess();
  const permissionsReady = access?.permissions !== undefined;
  const permissions = useResourcePermissions();
  const canManage = permissionsReady && resourceCan(permissions, "captures:manage");

  const capture = draft.spec.capture;
  const enabled = capture?.enabled ?? false;
  const storedSeconds = capture?.retentionSeconds;

  const [unit, setUnit] = useState<RetentionUnit>(() =>
    bestUnit(storedSeconds ?? DEFAULT_RETENTION_SECONDS),
  );
  const [rawValue, setRawValue] = useState<string>(() =>
    storedSeconds === undefined ? "" : String(storedSeconds / UNIT_SECONDS[unit]),
  );
  const [retentionError, setRetentionError] = useState<string | null>(null);

  useEffect(() => {
    onValidityChange?.(retentionError === null);
  }, [retentionError, onValidityChange]);

  const setCaptureField = <K extends keyof CaptureConfiguration>(
    key: K,
    value: CaptureConfiguration[K],
  ) => {
    const next: CaptureConfiguration = { ...(draft.spec.capture ?? {}), [key]: value };
    onChange({ ...draft, spec: { ...draft.spec, capture: next } });
  };

  const applyRetention = (valueStr: string, u: RetentionUnit) => {
    if (valueStr.trim() === "") {
      setRetentionError(null);
      setCaptureField("retentionSeconds", undefined);
      return;
    }
    const n = Number(valueStr);
    if (!Number.isFinite(n) || n <= 0) {
      setRetentionError(intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.enter_a_positive_number", defaultMessage: "Enter a positive number.", description: "Network capture: validation error under the retention window input when the value is not a positive number" }));
      return;
    }
    const seconds = Math.round(n * UNIT_SECONDS[u]);
    if (seconds > CLUSTER_MAX_RETENTION_SECONDS) {
      setRetentionError(
        intl.formatMessage(
          { id: "web.routes.tabs.settings.network_capture.exceeds_cluster_maximum", defaultMessage: "Exceeds the cluster maximum of 7 days ({seconds} seconds).", description: "Network capture: validation error when the retention window is longer than the cluster maximum; {seconds} is the maximum in seconds, formatted with thousands separators" },
          { seconds: fmt.number(CLUSTER_MAX_RETENTION_SECONDS) },
        ),
      );
      return;
    }
    setRetentionError(null);
    setCaptureField("retentionSeconds", seconds);
  };

  // ServerDetail handles selected-target access loading and errors. Fail closed
  // if this section has no resolved permissions, without showing a denial
  // warning before the selected target's permission result is available.
  const disabled = !canManage;

  return (
    <div className="space-y-8">
      <div className="space-y-4">
        <div>
          <Label className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.enable_capture", defaultMessage: "Enable Capture", description: "Label for the switch that turns network capture on or off" })}</Label>
          <Description className="text-sm">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.records_raw_network_protocol_traffic", defaultMessage: "Records raw network protocol traffic from a sidecar container for later download and analysis.", description: "Network capture: description under the Enable Capture heading saying it records raw network traffic from a sidecar for later download and analysis" })}</Description>
        </div>

        <div className="space-y-3">
          <div className="flex items-center gap-3">
            <Switch
              isSelected={enabled}
              isDisabled={disabled}
              onChange={(v) => setCaptureField("enabled", v)}
              aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.enable_capture", defaultMessage: "Enable Capture", description: "Label for the switch that turns network capture on or off" })}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
              </Switch.Content>
            </Switch>
            <span className="text-sm text-default-500">{enabled
              ? intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.enabled", defaultMessage: "Enabled", description: "Network capture: status label next to the enable switch when capture is on" })
              : intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.disabled", defaultMessage: "Disabled", description: "Network capture: status label next to the enable switch when capture is off" })}</span>
          </div>

          <p className="text-xs leading-relaxed text-default-500">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.turning_this_off_stops_any_running", defaultMessage: "Turning this off stops any running capture and blocks new ones immediately. The capture container itself stays in the pod, idle, until the pod is next recreated — Kubernetes has no API to remove an ephemeral container.", description: "Network capture: hint under the enabled status saying that turning capture off stops running captures and blocks new ones, though the idle capture container stays until the pod is recreated" })}</p>
          <p className="text-xs leading-relaxed text-default-500">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.network_packet_capture_requires_admin", defaultMessage: "Network packet capture requires admin access. Captures contain real player data (IP addresses, chat, credentials) and are not redacted.", description: "Network capture: warning under the capture status saying packet capture needs admin access and captures hold real player data that is not redacted" })}</p>
          {permissionsReady && !canManage && (
            <p className="text-xs leading-relaxed text-warning">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.you_don_t_have_permission_to_change", defaultMessage: "You don't have permission to change capture settings for this server.", description: "Network capture: warning shown to users who cannot change the capture settings for this server" })}</p>
          )}

          <div>
            <a href="/docs/capture" className="text-xs text-primary hover:underline">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.learn_more", defaultMessage: "Learn more", description: "Network capture: link at the bottom of the capture section that opens the capture documentation" })}</a>
          </div>
        </div>
      </div>

      <div className="space-y-4">
        <div>
          <Label className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.retention_window", defaultMessage: "Retention Window", description: "Section heading for how long captures are kept before automatic deletion" })}</Label>
          <Description className="text-sm">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.captures_are_automatically_deleted", defaultMessage: "Captures are automatically deleted after this window. Leave blank to use the cluster default.", description: "Network capture: description under the Retention Window heading saying captures are deleted after this window, and blank uses the cluster default" })}</Description>
        </div>

        <TextField isInvalid={retentionError !== null} className="space-y-3">
          <div className="flex items-center gap-2">
            <Input
              type="text"
              inputMode="numeric"
              disabled={disabled}
              value={rawValue}
              placeholder={String(DEFAULT_RETENTION_SECONDS / UNIT_SECONDS[unit])}
              onChange={(e) => {
                setRawValue(e.target.value);
                applyRetention(e.target.value, unit);
              }}
              aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.retention_window_value", defaultMessage: "Retention window value", description: "Network capture: accessible label of the box for the retention window amount; not shown as text; its placeholder is the default amount" })}
              className="w-24"
            />
            <Select
              selectedKey={unit}
              isDisabled={disabled}
              onSelectionChange={(key) => {
                const nextUnit = String(key) as RetentionUnit;
                setUnit(nextUnit);
                applyRetention(rawValue, nextUnit);
              }}
              className="w-32"
            >
              <Label className="sr-only">{intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.retention_window_unit", defaultMessage: "Retention window unit", description: "Label and accessible name for the retention window unit picker (seconds, minutes, hours, days)" })}</Label>
              <Select.Trigger
                aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.retention_window_unit", defaultMessage: "Retention window unit", description: "Label and accessible name for the retention window unit picker (seconds, minutes, hours, days)" })}
                className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm"
              >
                <Select.Value />
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover className="rounded border border-default-200">
                <ListBox aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.network_capture.retention_window_unit", defaultMessage: "Retention window unit", description: "Label and accessible name for the retention window unit picker (seconds, minutes, hours, days)" })}>
                  {UNIT_OPTIONS.map((opt) => (
                    <ListBoxItem key={opt.value} id={opt.value}>
                      {intl.formatMessage(opt.label)}
                    </ListBoxItem>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>
          </div>

          {retentionError ? (
            <FieldError className="text-xs">{retentionError}</FieldError>
          ) : (
            <p className="text-xs text-default-500">{intl.formatMessage(
              { id: "web.routes.tabs.settings.network_capture.cluster_maximum_7_days", defaultMessage: "Cluster maximum: 7 days ({seconds} seconds) — a storage-limitation-informed default, not a legal requirement.", description: "Network capture: hint under the retention window input stating the cluster maximum; {seconds} is the maximum in seconds, formatted with thousands separators" },
              { seconds: fmt.number(CLUSTER_MAX_RETENTION_SECONDS) },
            )}</p>
          )}
        </TextField>
      </div>
    </div>
  );
}
