import { Input, Button, Switch } from "@heroui/react";
import { defineMessages, type MessageDescriptor } from "react-intl";
import type { IdleSpec, Probe, ProbeKind, ProbeSet } from "@/types";
import { Field } from "./Field";
import { GRACE_PERIOD_ANNOTATION, type SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";

// Mirrors the CRD's structural guard on IdleSpec.WakeWindows: a five (or six,
// with seconds) whitespace-separated field cron expression, at least 9
// characters. A blank or malformed entry is flagged inline rather than
// filtered out — filtering would delete the row out from under a user still
// mid-typing it.
const WAKE_WINDOW_PATTERN = /^\S+\s+\S+\s+\S+\s+\S+\s+\S+(\s+\S+)?$/;
function isValidWakeWindow(w: string): boolean {
  return w.length >= 9 && WAKE_WINDOW_PATTERN.test(w);
}

const PROBE_FIELD_LABELS = defineMessages({
  initialDelaySeconds: { id: "web.routes.tabs.settings.lifecycle.probe_initial_delay", defaultMessage: "Initial delay", description: "Lifecycle: label of the health probe initial delay input" },
  periodSeconds: { id: "web.routes.tabs.settings.lifecycle.probe_period", defaultMessage: "Period", description: "Lifecycle: label of the health probe period input" },
  timeoutSeconds: { id: "web.routes.tabs.settings.lifecycle.probe_timeout", defaultMessage: "Timeout", description: "Lifecycle: label of the health probe timeout input" },
  failureThreshold: { id: "web.routes.tabs.settings.lifecycle.probe_failure_threshold", defaultMessage: "Failure threshold", description: "Lifecycle: label of the health probe failure threshold input" },
  successThreshold: { id: "web.routes.tabs.settings.lifecycle.probe_success_threshold", defaultMessage: "Success threshold", description: "Lifecycle: label of the health probe success threshold input" },
});

const PROBE_FIELDS: { key: keyof Probe; label: MessageDescriptor }[] = [
  { key: "initialDelaySeconds", label: PROBE_FIELD_LABELS.initialDelaySeconds },
  { key: "periodSeconds", label: PROBE_FIELD_LABELS.periodSeconds },
  { key: "timeoutSeconds", label: PROBE_FIELD_LABELS.timeoutSeconds },
  { key: "failureThreshold", label: PROBE_FIELD_LABELS.failureThreshold },
  { key: "successThreshold", label: PROBE_FIELD_LABELS.successThreshold },
];

const PROBE_KIND_LABELS = defineMessages({
  readiness: { id: "web.routes.tabs.settings.lifecycle.probe_kind_readiness", defaultMessage: "readiness", description: "Lifecycle: name of the readiness health probe, shown as a lowercase label (CSS capitalizes it)" },
  liveness: { id: "web.routes.tabs.settings.lifecycle.probe_kind_liveness", defaultMessage: "liveness", description: "Lifecycle: name of the liveness health probe, shown as a lowercase label (CSS capitalizes it)" },
  startup: { id: "web.routes.tabs.settings.lifecycle.probe_kind_startup", defaultMessage: "startup", description: "Lifecycle: name of the startup health probe, shown as a lowercase label (CSS capitalizes it)" },
});

const PROBE_KINDS: ProbeKind[] = ["readiness", "liveness", "startup"];

export function LifecycleSection({ draft, onChange, template }: SectionProps) {
  const intl = useOptionalIntl();
  const annotations = draft.metadata.annotations ?? {};
  const autoRestart = !(draft.spec.suspend ?? false);
  // Read the real spec field; fall back to the legacy annotation for servers
  // created before the migration so their value still shows (display only —
  // we don't mutate the draft on mount, so this doesn't mark the form dirty).
  const grace =
    draft.spec.stopGracePeriodSeconds !== undefined
      ? String(draft.spec.stopGracePeriodSeconds)
      : (annotations[GRACE_PERIOD_ANNOTATION] ?? "");
  const templateProbes = template?.spec.probes;
  const overrides = draft.spec.probes;

  // setProbe writes a per-server override for `kind`, seeded from the
  // template probe so the action (httpGet/tcpSocket/exec) is preserved.
  const setProbe = (kind: ProbeKind, field: keyof Probe, raw: string) => {
    const base = overrides?.[kind] ?? templateProbes?.[kind] ?? {};
    const next: Probe = { ...base };
    if (raw === "") delete next[field];
    else next[field] = Number(raw);
    const probes: ProbeSet = { ...overrides, [kind]: next };
    onChange({ ...draft, spec: { ...draft.spec, probes } });
  };

  const resetProbe = (kind: ProbeKind) => {
    if (!overrides) return;
    const probes: ProbeSet = { ...overrides };
    delete probes[kind];
    onChange({
      ...draft,
      spec: {
        ...draft.spec,
        probes: Object.keys(probes).length ? probes : undefined,
      },
    });
  };

  const setSuspend = (suspend: boolean) => {
    onChange({
      ...draft,
      spec: { ...draft.spec, suspend },
    });
  };

  const setGrace = (value: string) => {
    // Write the field the operator actually reads, and migrate off the dead
    // legacy annotation on first edit.
    const spec = { ...draft.spec };
    if (value === "") delete spec.stopGracePeriodSeconds;
    else spec.stopGracePeriodSeconds = Number(value);
    const nextAnnotations = { ...annotations };
    delete nextAnnotations[GRACE_PERIOD_ANNOTATION];
    onChange({
      ...draft,
      spec,
      metadata: {
        ...draft.metadata,
        annotations: Object.keys(nextAnnotations).length ? nextAnnotations : undefined,
      },
    });
  };

  const setIdle = (idle?: IdleSpec) => {
    const spec = { ...draft.spec };
    if (!idle || !idle.enabled) {
      delete spec.idle;
    } else {
      spec.idle = idle;
    }
    onChange({
      ...draft,
      spec,
    });
  };

  const addWakeWindow = () => {
    const idle = draft.spec.idle ?? { enabled: true };
    const windows = idle.wakeWindows ?? [];
    if (windows.length < 8) {
      setIdle({
        ...idle,
        wakeWindows: [...windows, ""],
      });
    }
  };

  const removeWakeWindow = (index: number) => {
    const idle = draft.spec.idle ?? { enabled: true };
    const windows = idle.wakeWindows ?? [];
    setIdle({
      ...idle,
      wakeWindows: windows.filter((_, i) => i !== index),
    });
  };

  const setWakeWindow = (index: number, value: string) => {
    const idle = draft.spec.idle ?? { enabled: true };
    // Copy before writing: `windows` aliases the draft's own array, and
    // mutating it in place would edit state React never sees change.
    const windows = [...(idle.wakeWindows ?? [])];
    windows[index] = value;
    setIdle({
      ...idle,
      wakeWindows: windows,
    });
  };

  const setIdleEnabled = (enabled: boolean) => {
    const idle = draft.spec.idle;
    if (!enabled) {
      // Keep the user's windows/threshold — `enabled: false` is exactly
      // what the operator reads as disabled (idleDecide's first branch),
      // and re-enabling must not start the user back from scratch.
      onChange({
        ...draft,
        spec: { ...draft.spec, idle: idle ? { ...idle, enabled: false } : undefined },
      });
      return;
    }
    setIdle({ enabled: true, afterMinutes: idle?.afterMinutes ?? 30, wakeWindows: idle?.wakeWindows ?? [] });
  };

  const setIdleAfterMinutes = (value: string) => {
    const idle = draft.spec.idle ?? { enabled: true };
    const minutes = value === "" ? undefined : Number(value);
    setIdle({
      ...idle,
      afterMinutes: minutes,
    });
  };

  const graceNumber = Number(grace);
  const graceInvalid =
    grace !== "" &&
    (!Number.isFinite(graceNumber) || graceNumber < 0 || graceNumber > 600 || !Number.isInteger(graceNumber));

  // Mirrors graceInvalid: the CRD enforces 5–1440 at admission, so a value
  // outside it here would otherwise sail past Save and fail the whole PUT,
  // discarding every other edit in the draft.
  const idleMinutes = draft.spec.idle?.afterMinutes;
  const idleMinutesText = idleMinutes !== undefined ? String(idleMinutes) : "";
  const idleInvalid =
    draft.spec.idle?.enabled === true &&
    idleMinutes !== undefined &&
    (!Number.isInteger(idleMinutes) || idleMinutes < 5 || idleMinutes > 1440);

  return (
    <div className="space-y-6">
      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.auto_restart", defaultMessage: "Auto-restart", description: "Lifecycle: field label for the switch that restarts the server automatically; the same text is its accessible name" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.when_off_the_statefulset_is_scaled_to", defaultMessage: "When off, the StatefulSet is scaled to zero (Suspended). Data is preserved.", description: "Lifecycle: hint under the auto-restart switch saying that when it is off the server is suspended and its data is kept" })}
      >
        <div className="flex items-center gap-3 pt-1">
          <Switch isSelected={autoRestart} onChange={(v) => setSuspend(!v)} aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.auto_restart", defaultMessage: "Auto-restart", description: "Lifecycle: field label for the switch that restarts the server automatically; the same text is its accessible name" })}>
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
          <span className="text-sm text-muted">
            {autoRestart
              ? intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.pod_desired_running", defaultMessage: "Pod is desired Running", description: "Lifecycle: status text next to the auto-restart switch when on" })
              : intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.pod_suspended", defaultMessage: "Pod is suspended", description: "Lifecycle: status text next to the auto-restart switch when off" })}
          </span>
        </div>
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.termination_grace_period", defaultMessage: "Termination grace period", description: "Lifecycle: field label for the number of seconds given to the server to stop cleanly before it is shut down" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.seconds_the_operator_waits_for_the", defaultMessage: "Seconds the operator waits for the template's stop sequence to finish before scaling the pod down. Range 0–600; defaults to 30. No effect when the template declares no stop sequence.", description: "Lifecycle: hint under the termination grace period field giving the allowed range 0 to 600 and the default of 30 seconds" })}
      >
        <div className="flex items-center gap-2">
          <Input
            value={grace}
            onChange={(e) => setGrace(e.target.value.replace(/[^0-9]/g, ""))}
            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.grace_placeholder", defaultMessage: "120", description: "Lifecycle: example termination grace period in seconds, shown as the input placeholder" })}
            inputMode="numeric"
            aria-invalid={graceInvalid || undefined}
            className="max-w-32"
          />
          <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.seconds", defaultMessage: "seconds", description: "Unit label after the termination grace period input" })}</span>
        </div>
        {graceInvalid && (
          <div className="pt-1 text-xs text-danger">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.must_be_an_integer_between_0_and_600", defaultMessage: "Must be an integer between 0 and 600.", description: "Lifecycle: validation error under the grace period field when the value is not a whole number from 0 to 600" })}</div>
        )}
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.health_probes", defaultMessage: "Health probes", description: "Lifecycle: field label for the per-server overrides of the health probe timing" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.per_server_overrides_for_the_template_s", defaultMessage: "Per-server overrides for the template's readiness/liveness/startup probe timing. The probe action is inherited from the template.", description: "Lifecycle: hint under the health probes label explaining the overrides for probe timing; the probe action comes from the template" })}
      >
        <div className="space-y-4">
          {PROBE_KINDS.map((kind) => {
            const tmplProbe = templateProbes?.[kind];
            const override = overrides?.[kind];
            const effective = override ?? tmplProbe;
            if (!tmplProbe && !override) {
              return (
                <div key={kind} className="text-xs text-muted">
                  <span className="font-medium capitalize text-fg">{intl.formatMessage(PROBE_KIND_LABELS[kind])}</span>{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.not_defined_by_the_template", defaultMessage: ": not defined by the template.", description: "Lifecycle: text after a probe kind name, shown when neither the template nor this server defines that probe" })}</div>
              );
            }
            return (
              <div key={kind} className="rounded border border-border p-3">
                <div className="flex items-center justify-between pb-2">
                  <span className="text-sm font-medium capitalize">{intl.formatMessage(PROBE_KIND_LABELS[kind])}</span>
                  {override ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      onPress={() => resetProbe(kind)}
                    >{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.reset_to_template", defaultMessage: "Reset to template", description: "Button that discards the per-server probe override and restores the template probe" })}</Button>
                  ) : (
                    <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.from_template", defaultMessage: "from template", description: "Badge showing that a health probe value comes from the template, not a per-server override" })}</span>
                  )}
                </div>
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                  {PROBE_FIELDS.map((f) => (
                    <div key={String(f.key)} className="flex flex-col gap-1">
                      <label className="text-[11px] text-muted">{intl.formatMessage(f.label)}</label>
                      <Input
                        inputMode="numeric"
                        value={String((effective?.[f.key] as number | undefined) ?? "")}
                        onChange={(e) =>
                          setProbe(kind, f.key, e.target.value.replace(/[^0-9]/g, ""))
                        }
                        placeholder={intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                        aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.probe_field_aria", defaultMessage: "{kind} {label}", description: "Lifecycle: aria-label of a health probe timing input; {kind} is the probe kind (readiness, liveness or startup) and {label} is the field name" }, { kind: intl.formatMessage(PROBE_KIND_LABELS[kind]), label: intl.formatMessage(f.label) })}
                      />
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.idle_auto_sleep", defaultMessage: "Idle auto-sleep", description: "Lifecycle: field label for the idle auto-sleep setting that scales the server to zero when nobody is playing" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.when_enabled_scales_the_server_to_zero", defaultMessage: "When enabled, scales the server to zero after it reports zero players for the configured duration. A wake window cron or an explicit :wake brings it back.", description: "Lifecycle: hint under the idle auto-sleep label explaining the server sleeps after zero players for the set time and wake windows bring it back" })}
      >
        <div className="space-y-3">
          <div className="flex items-center gap-3 pt-1">
            <Switch
              isSelected={draft.spec.idle?.enabled ?? false}
              onChange={setIdleEnabled}
              aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.enable_idle_auto_sleep", defaultMessage: "Enable idle auto-sleep", description: "Lifecycle: switch that turns idle auto-sleep on or off; the text is its accessible name, not shown as text" })}
            >
              <Switch.Content>
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
              </Switch.Content>
            </Switch>
            <span className="text-sm text-muted">
              {draft.spec.idle?.enabled
                ? intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.idle_auto_sleep_enabled", defaultMessage: "Idle auto-sleep enabled", description: "Lifecycle: status text next to the idle auto-sleep switch when on" })
                : intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.disabled", defaultMessage: "Disabled", description: "Lifecycle: status text next to the idle auto-sleep switch when off" })}
            </span>
          </div>

          {draft.spec.idle?.enabled && (
            <div className="space-y-3 rounded border border-border p-3">
              <div className="space-y-1">
                <label className="text-xs font-medium text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.sleep_after", defaultMessage: "Sleep after", description: "Lifecycle: small label in front of the minutes box that sets how long the server waits with zero players before sleeping" })}</label>
                <Input
                  value={idleMinutesText}
                  onChange={(e) =>
                    setIdleAfterMinutes(e.target.value.replace(/[^0-9]/g, ""))
                  }
                  placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.idle_minutes_placeholder", defaultMessage: "30", description: "Lifecycle: example idle sleep duration in minutes, shown as the input placeholder" })}
                  inputMode="numeric"
                  aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.idle_sleep_after", defaultMessage: "Idle sleep after", description: "Lifecycle: accessible label of the minutes box for the idle sleep delay; not shown as text" })}
                  aria-invalid={idleInvalid || undefined}
                />
                <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.minutes_of_zero_players_5_1440", defaultMessage: "minutes of zero players (5–1440)", description: "Lifecycle: unit text after the minutes box, saying the value counts minutes of zero players and must be from 5 to 1440" })}</span>
                {idleInvalid && (
                  <div className="pt-1 text-xs text-danger">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.must_be_an_integer_between_5_and_1440", defaultMessage: "Must be an integer between 5 and 1440.", description: "Lifecycle: validation error under the idle minutes box when the value is not a whole number from 5 to 1440" })}</div>
                )}
              </div>

              <div>
                <div className="mb-2 flex items-center justify-between">
                  <label className="text-xs font-medium text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.wake_windows", defaultMessage: "Wake windows", description: "Lifecycle: field label for the list of wake windows, the times the server is woken on a schedule" })}</label>
                  <Button
                    variant="ghost"
                    size="sm"
                    onPress={addWakeWindow}
                    isDisabled={(draft.spec.idle?.wakeWindows?.length ?? 0) >= 8}
                  >{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.add_wake_window", defaultMessage: "Add wake window", description: "Button that adds a wake window cron row (up to 8)" })}</Button>
                </div>
                <div className="space-y-2">
                  {(draft.spec.idle?.wakeWindows ?? []).map((cron, i) => {
                    const invalid = !isValidWakeWindow(cron);
                    return (
                      <div key={i} className="space-y-1">
                        <div className="flex gap-2">
                          <Input
                            value={cron}
                            onChange={(e) => setWakeWindow(i, e.target.value)}
                            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.wake_window_placeholder", defaultMessage: "0 9 * * 1-5", description: "Lifecycle: example wake window cron expression, shown as the input placeholder" })}
                            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.wake_window_aria", defaultMessage: "Wake window {n}", description: "Lifecycle: aria-label of a wake window cron input; {n} is the 1-based position of the window" }, { n: i + 1 })}
                            aria-invalid={invalid || undefined}
                            className="font-mono text-xs"
                          />
                          <Button
                            variant="ghost"
                            size="sm"
                            onPress={() => removeWakeWindow(i)}
                          >{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.remove", defaultMessage: "Remove", description: "Button that removes this wake window" })}</Button>
                        </div>
                        {invalid && (
                          <div className="text-xs text-danger">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.must_be_a_five_field_cron_expression_e", defaultMessage: "Must be a five-field cron expression (e.g. 0 9 * * 1-5).", description: "Lifecycle: validation error under a wake window when its text is not a five-field cron expression; the example 0 9 * * 1-5 is shown" })}</div>
                        )}
                      </div>
                    );
                  })}
                </div>
                <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.standard_five_field_cron_max_8_entries", defaultMessage: "Standard five-field cron. Max 8 entries.", description: "Lifecycle: hint under the wake windows list saying the entries use standard five-field cron and at most 8 are allowed" })}</span>
              </div>

              <div className="space-y-1 rounded bg-surface/40 p-2">
                <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.a_game_that_reports_no_player_count", defaultMessage: "A game that reports no player count will never sleep.", description: "Lifecycle: warning note that a game reporting no player count will never sleep, since idle detection cannot work" })}</div>
                <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.lifecycle.a_wake_window_never_restarts_a_server", defaultMessage: "A wake window never restarts a server you stopped by hand.", description: "Lifecycle: note that a wake window does not restart a server the user stopped by hand" })}</div>
              </div>
            </div>
          )}
        </div>
      </Field>
    </div>
  );
}
