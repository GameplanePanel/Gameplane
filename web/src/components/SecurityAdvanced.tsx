import { useCallback, useEffect, useId, useState, type ReactNode } from "react";
import { Alert, Chip, Input, ListBox, ListBoxItem, Select } from "@heroui/react";
import { ChevronDown, ChevronRight, TriangleAlert } from "lucide-react";
import type { IntlShape } from "react-intl";
import type {
  GameServerSecurityStatus,
  GameServerSecuritySpec,
  GameTemplate,
  SecurityIDOverride,
  SecuritySetting,
  SecuritySource,
  VolumeOwnershipCheck,
} from "@/types";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { useOptionalIntl } from "@/lib/i18n/format";

// Spec 030: per-server security overrides (runAsUser, runAsGroup, fsGroup,
// volumeOwnershipCheck) with module defaults, applied status and root-override
// notices. The operator is the authority; this panel only edits spec.security.

type IdSetting = "runAsUser" | "runAsGroup" | "fsGroup";
const ID_SETTINGS: readonly IdSetting[] = ["runAsUser", "runAsGroup", "fsGroup"];
const SETTINGS: readonly SecuritySetting[] = ["runAsUser", "runAsGroup", "fsGroup", "volumeOwnershipCheck"];
const MAX_SECURITY_ID = 4294967295;

type SettingValue = GameServerSecuritySpec[SecuritySetting];

export interface SecurityAdvancedProps {
  value: GameServerSecuritySpec | undefined;
  onChange: (next: GameServerSecuritySpec | undefined) => void;
  template?: GameTemplate;
  status?: GameServerSecurityStatus;
  heldBack?: { running: boolean; message?: string };
  readOnly?: boolean;
  defaultOpen?: boolean;
  onValidityChange?: (valid: boolean) => void;
}

export function isValidSecurityId(v: number): boolean {
  return Number.isInteger(v) && v >= 0 && v <= MAX_SECURITY_ID;
}

function idOverrideValid(o: SecurityIDOverride | undefined): boolean {
  if (o === undefined) return true;
  if (o.useImageDefault === true) return o.value === undefined;
  return o.value !== undefined && isValidSecurityId(o.value);
}

export function securityOverrideCount(value?: GameServerSecuritySpec): number {
  if (!value) return 0;
  return SETTINGS.filter((s) => value[s] !== undefined).length;
}

export function securityIsValid(value?: GameServerSecuritySpec): boolean {
  if (!value) return true;
  return idOverrideValid(value.runAsUser) && idOverrideValid(value.runAsGroup) && idOverrideValid(value.fsGroup);
}

function parseSecurityId(text: string): number | undefined {
  const s = text.trim();
  if (!/^\d+$/.test(s)) return undefined;
  const n = Number(s);
  return isValidSecurityId(n) ? n : undefined;
}

// Returns the spec with one key set (or removed); undefined when no key is left.
function withSetting<K extends SecuritySetting>(
  value: GameServerSecuritySpec | undefined,
  key: K,
  next: GameServerSecuritySpec[K] | undefined,
): GameServerSecuritySpec | undefined {
  const out: GameServerSecuritySpec = { ...value };
  if (next === undefined) delete out[key];
  else out[key] = next;
  return Object.keys(out).length > 0 ? out : undefined;
}

function checkLabel(intl: IntlShape, c: VolumeOwnershipCheck): string {
  return c === "EveryStart"
    ? intl.formatMessage({ id: "web.routes.server_settings.security_check_every_start", defaultMessage: "Every start", description: "Advanced security: volume ownership check that runs on every start" })
    : intl.formatMessage({ id: "web.routes.server_settings.security_check_only_when_wrong", defaultMessage: "Only when wrong", description: "Advanced security: volume ownership check that runs only when the data volume's group is wrong" });
}

function sourceLabel(intl: IntlShape, source: SecuritySource): string {
  switch (source) {
    case "ModuleDefault":
      return intl.formatMessage({ id: "web.routes.server_settings.security_source_module_default", defaultMessage: "Module default", description: "Advanced security: source chip for a value that comes from the game module's default" });
    case "Override":
      return intl.formatMessage({ id: "web.routes.server_settings.security_source_override", defaultMessage: "Override", description: "Advanced security: source chip for a value the server owner overrode" });
    case "ImageDefaultOverride":
      return intl.formatMessage({ id: "web.routes.server_settings.security_source_image_default_override", defaultMessage: "Image default (override)", description: "Advanced security: source chip for an override that asks for the container image's default user or group" });
    case "ImageDefault":
      return intl.formatMessage({ id: "web.routes.server_settings.security_source_image_default", defaultMessage: "Image default", description: "Advanced security: source chip for the container image's default user or group" });
    case "BuiltInDefault":
      return intl.formatMessage({ id: "web.routes.server_settings.security_source_built_in_default", defaultMessage: "Built-in default", description: "Advanced security: source chip for the default built into Gameplane" });
  }
}

interface StatusEntry {
  value?: number | VolumeOwnershipCheck;
  source: SecuritySource;
}

function entryText(intl: IntlShape, e: StatusEntry): string {
  if (typeof e.value === "number") return String(e.value);
  if (e.value === "EveryStart" || e.value === "OnlyWhenWrong") return checkLabel(intl, e.value);
  return intl.formatMessage({ id: "web.routes.server_settings.security_value_image_default", defaultMessage: "image default", description: "Advanced security: value shown when a security ID is the container image's default, not a number" });
}

function entryKey(e: StatusEntry | undefined): string | undefined {
  if (e === undefined) return undefined;
  return e.value === undefined ? "image" : String(e.value);
}

function SettingRow({ label, help, moduleDefault, children, status, chips }: {
  label: string;
  help: string;
  moduleDefault: string;
  children: ReactNode;
  status?: ReactNode;
  chips?: ReactNode;
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-[200px_1fr]">
      <div className="space-y-1">
        <p className="text-sm font-medium">{label}</p>
        <p className="text-xs text-muted">{help}</p>
      </div>
      <div className="space-y-2">
        <p className="text-xs text-muted">{moduleDefault}</p>
        {children}
        {status}
        {chips}
      </div>
    </div>
  );
}

function SettingStatus({ applied, desired }: { applied?: StatusEntry; desired?: StatusEntry }) {
  const intl = useOptionalIntl();
  const shown = applied ?? desired;
  if (shown === undefined) return null;
  const shownText = entryText(intl, shown);
  // Pending means a saved change the running server has not applied yet, so a
  // server that has never started (no applied record) shows none (FR-009).
  const pending = applied !== undefined && desired !== undefined && entryKey(desired) !== entryKey(applied);
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
      <span>
        {applied !== undefined
          ? intl.formatMessage({ id: "web.routes.server_settings.security_applied", defaultMessage: "Applied: {value}", description: "Advanced security: status line showing the value the running server applied; {value} is the value, a number or a word such as image default" }, { value: shownText })
          : intl.formatMessage({ id: "web.routes.server_settings.security_next_start", defaultMessage: "Next start: {value}", description: "Advanced security: status line showing the value the server will use on its next start, before it has ever started; {value} is the value" }, { value: shownText })}
      </span>
      <Chip size="sm" variant="soft">{sourceLabel(intl, shown.source)}</Chip>
      {pending && desired !== undefined && (
        <Chip size="sm" variant="soft" color="warning">
          {intl.formatMessage({ id: "web.routes.server_settings.security_pending", defaultMessage: "Pending: {value} · applies on the next restart", description: "Advanced security: warning chip for a saved value that is not applied yet; {value} is the saved value; the middle dot separates it from the rest of the sentence" }, { value: entryText(intl, desired) })}
        </Chip>
      )}
    </div>
  );
}

interface IdControlsProps {
  setting: IdSetting;
  label: string;
  override: SecurityIDOverride | undefined;
  readOnly: boolean;
  onPropose: (next: SecurityIDOverride | undefined) => void;
  onValidity: (setting: IdSetting, valid: boolean) => void;
}

function IdControls({ setting, label, override, readOnly, onPropose, onValidity }: IdControlsProps) {
  const intl = useOptionalIntl();
  const errorId = useId();
  const isImage = override?.useImageDefault === true;
  const committed = isImage ? undefined : override?.value;
  const [drafting, setDrafting] = useState(false);
  const [text, setText] = useState(() => (committed === undefined ? "" : String(committed)));
  const [seen, setSeen] = useState(committed);
  // Follow the saved value when it changes from outside this row (or after our own commit).
  if (seen !== committed) {
    setSeen(committed);
    setText(committed === undefined ? "" : String(committed));
    setDrafting(false);
  }
  const custom = drafting || (override !== undefined && !isImage);
  const parsed = parseSecurityId(text);
  const valid = !custom || parsed !== undefined;
  useEffect(() => {
    onValidity(setting, valid);
  }, [valid, setting, onValidity]);

  const defaultText = intl.formatMessage({ id: "web.routes.server_settings.security_mode_module_default", defaultMessage: "Module default", description: "Advanced security: option in the setting's dropdown that keeps the module's default value" });
  const customText = intl.formatMessage({ id: "web.routes.server_settings.security_mode_custom_value", defaultMessage: "Custom value", description: "Advanced security: option in the setting's dropdown that lets the owner type a numeric ID" });
  const imageText = intl.formatMessage({ id: "web.routes.server_settings.security_mode_use_image_default", defaultMessage: "Use image default", description: "Advanced security: option in the setting's dropdown that uses the container image's default user or group" });

  if (readOnly) {
    return (
      <p className="text-sm">{isImage ? imageText : committed !== undefined ? String(committed) : defaultText}</p>
    );
  }

  const commit = () => {
    if (parsed === undefined) return;
    if (parsed === committed) {
      setDrafting(false);
      return;
    }
    onPropose({ value: parsed });
  };

  const choose = (key: string) => {
    if (key === "custom") {
      setDrafting(true);
      setText("");
      return;
    }
    setDrafting(false);
    onPropose(key === "image" ? { useImageDefault: true } : undefined);
  };

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Select
          aria-label={label}
          selectedKey={custom ? "custom" : isImage ? "image" : "default"}
          onSelectionChange={(key) => choose(String(key))}
          className="w-48"
        >
          <Select.Trigger>
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox aria-label={label}>
              <ListBoxItem id="default" textValue={defaultText}>{defaultText}</ListBoxItem>
              <ListBoxItem id="custom" textValue={customText}>{customText}</ListBoxItem>
              <ListBoxItem id="image" textValue={imageText}>{imageText}</ListBoxItem>
            </ListBox>
          </Select.Popover>
        </Select>
        {custom && (
          <Input
            type="number"
            inputMode="numeric"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => {
              if (e.key === "Enter") commit();
            }}
            aria-label={intl.formatMessage({ id: "web.routes.server_settings.security_value_input_label", defaultMessage: "{setting} value", description: "Advanced security: accessible label of the number box for a setting's numeric ID; {setting} is the setting name, such as Run-as user" }, { setting: label })}
            aria-invalid={parsed === undefined}
            aria-describedby={parsed === undefined ? errorId : undefined}
            className="w-36"
          />
        )}
      </div>
      {custom && parsed === undefined && (
        <p id={errorId} className="text-xs text-danger">
          {intl.formatMessage({ id: "web.routes.server_settings.security_id_range_error", defaultMessage: "Enter a whole number from 0 to 4294967295.", description: "Advanced security: error under a numeric ID box when the entry is not a whole number from 0 to 4294967295" })}
        </p>
      )}
    </div>
  );
}

function OwnershipControls({ label, value, readOnly, onPropose }: {
  label: string;
  value: VolumeOwnershipCheck | undefined;
  readOnly: boolean;
  onPropose: (next: VolumeOwnershipCheck | undefined) => void;
}) {
  const intl = useOptionalIntl();
  const defaultText = intl.formatMessage({ id: "web.routes.server_settings.security_mode_module_default", defaultMessage: "Module default", description: "Advanced security: option in the setting's dropdown that keeps the module's default value" });
  if (readOnly) {
    return <p className="text-sm">{value === undefined ? defaultText : checkLabel(intl, value)}</p>;
  }
  const everyText = checkLabel(intl, "EveryStart");
  const onceText = checkLabel(intl, "OnlyWhenWrong");
  return (
    <Select
      aria-label={label}
      selectedKey={value ?? "default"}
      onSelectionChange={(key) => {
        const k = String(key);
        onPropose(k === "EveryStart" ? "EveryStart" : k === "OnlyWhenWrong" ? "OnlyWhenWrong" : undefined);
      }}
      className="w-48"
    >
      <Select.Trigger>
        <Select.Value />
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover>
        <ListBox aria-label={label}>
          <ListBoxItem id="default" textValue={defaultText}>{defaultText}</ListBoxItem>
          <ListBoxItem id="EveryStart" textValue={everyText}>{everyText}</ListBoxItem>
          <ListBoxItem id="OnlyWhenWrong" textValue={onceText}>{onceText}</ListBoxItem>
        </ListBox>
      </Select.Popover>
    </Select>
  );
}

interface PendingRisk {
  setting: SecuritySetting;
  next: NonNullable<SettingValue>;
  reason?: string;
}

export function SecurityAdvanced({
  value,
  onChange,
  template,
  status,
  heldBack,
  readOnly = false,
  defaultOpen = false,
  onValidityChange,
}: SecurityAdvancedProps) {
  const intl = useOptionalIntl();
  const panelId = useId();
  const [open, setOpen] = useState(defaultOpen);
  const [rowInvalid, setRowInvalid] = useState<Partial<Record<IdSetting, boolean>>>({});
  const [pending, setPending] = useState<PendingRisk | null>(null);
  // Bumped on cancel so the numeric rows remount and drop their unsaved text.
  const [resetCount, setResetCount] = useState(0);

  const valid = securityIsValid(value) && !ID_SETTINGS.some((s) => rowInvalid[s] === true);
  useEffect(() => {
    onValidityChange?.(valid);
  }, [valid, onValidityChange]);

  const reportRowValidity = useCallback((setting: IdSetting, ok: boolean) => {
    setRowInvalid((prev) => (prev[setting] === !ok ? prev : { ...prev, [setting]: !ok }));
  }, []);

  const labels: Record<SecuritySetting, string> = {
    runAsUser: intl.formatMessage({ id: "web.routes.server_settings.security_run_as_user", defaultMessage: "Run-as user", description: "Advanced security: label of the setting for the user ID the game process runs as" }),
    runAsGroup: intl.formatMessage({ id: "web.routes.server_settings.security_run_as_group", defaultMessage: "Run-as group", description: "Advanced security: label of the setting for the group ID the game process runs as" }),
    fsGroup: intl.formatMessage({ id: "web.routes.server_settings.security_fs_group", defaultMessage: "Filesystem group", description: "Advanced security: label of the setting for the group that owns the data volume" }),
    volumeOwnershipCheck: intl.formatMessage({ id: "web.routes.server_settings.security_volume_ownership_check", defaultMessage: "Volume ownership check", description: "Advanced security: label of the setting that decides when the data volume's group is fixed on start" }),
  };
  const helps: Record<SecuritySetting, string> = {
    runAsUser: intl.formatMessage({ id: "web.routes.server_settings.security_run_as_user_help", defaultMessage: "User ID the game process runs as.", description: "Advanced security: help text under the Run-as user label" }),
    runAsGroup: intl.formatMessage({ id: "web.routes.server_settings.security_run_as_group_help", defaultMessage: "Group ID the game process runs as.", description: "Advanced security: help text under the Run-as group label" }),
    fsGroup: intl.formatMessage({ id: "web.routes.server_settings.security_fs_group_help", defaultMessage: "Group that owns the data volume, so the game can write to it.", description: "Advanced security: help text under the Filesystem group label" }),
    volumeOwnershipCheck: intl.formatMessage({ id: "web.routes.server_settings.security_volume_ownership_check_help", defaultMessage: "When the data volume's group is fixed on start.", description: "Advanced security: help text under the Volume ownership check label" }),
  };

  const risky = template?.spec.security?.risky;
  const imageDefaultText = intl.formatMessage({ id: "web.routes.server_settings.security_value_image_default", defaultMessage: "image default", description: "Advanced security: value shown when a security ID is the container image's default, not a number" });

  const apply = (setting: SecuritySetting, next: SettingValue) => {
    onChange(withSetting(value, setting, next));
  };

  // Every change goes through here so a risky setting asks first (OD-6).
  const propose = (setting: SecuritySetting, next: SettingValue) => {
    if (JSON.stringify(value?.[setting] ?? null) === JSON.stringify(next ?? null)) return;
    const note = next !== undefined ? risky?.[setting] : undefined;
    if (next !== undefined && note !== undefined) {
      setPending({ setting, next, reason: note.reason });
      return;
    }
    apply(setting, next);
  };

  const confirmRisk = () => {
    if (pending) apply(pending.setting, pending.next);
    setPending(null);
  };

  const cancelRisk = () => {
    setPending(null);
    setResetCount((c) => c + 1);
  };

  const count = securityOverrideCount(value);
  const applied = status?.applied;
  const desired = status?.desired;

  const idRow = (s: IdSetting) => {
    const moduleValue = template?.spec.security?.[s];
    const override = value?.[s];
    return (
      <SettingRow
        key={`${s}-${resetCount}`}
        label={labels[s]}
        help={helps[s]}
        moduleDefault={intl.formatMessage({ id: "web.routes.server_settings.security_module_default_value", defaultMessage: "Module default: {value}", description: "Advanced security: line under a setting that shows the value the game module sets; {value} is a number or the words image default" }, { value: moduleValue !== undefined ? String(moduleValue) : imageDefaultText })}
        status={status ? <SettingStatus applied={applied?.[s]} desired={desired?.[s]} /> : undefined}
        chips={risky?.[s] !== undefined && override !== undefined ? (
          <Chip size="sm" variant="soft" color="warning">
            {intl.formatMessage({ id: "web.routes.server_settings.security_risky", defaultMessage: "Risky", description: "Advanced security: warning chip on a setting the module marks as risky when the owner has overridden it" })}
          </Chip>
        ) : undefined}
      >
        <IdControls
          setting={s}
          label={labels[s]}
          override={override}
          readOnly={readOnly}
          onPropose={(next) => propose(s, next)}
          onValidity={reportRowValidity}
        />
      </SettingRow>
    );
  };

  const fsOverride = value?.fsGroup;
  const effectiveFsGroup = fsOverride?.value ?? (fsOverride?.useImageDefault === true ? undefined : template?.spec.security?.fsGroup);
  const noFsGroup = status?.desired !== undefined
    ? status.desired.fsGroup?.value === undefined
    : effectiveFsGroup === undefined;
  const checkOverride = value?.volumeOwnershipCheck;
  const moduleCheck = template?.spec.security?.volumeOwnershipCheck;

  const ownershipRow = (
    <SettingRow
      key={`volumeOwnershipCheck-${resetCount}`}
      label={labels.volumeOwnershipCheck}
      help={helps.volumeOwnershipCheck}
      moduleDefault={intl.formatMessage({ id: "web.routes.server_settings.security_module_default_value", defaultMessage: "Module default: {value}", description: "Advanced security: line under a setting that shows the value the game module sets; {value} is a number or the words image default" }, { value: moduleCheck !== undefined ? checkLabel(intl, moduleCheck) : intl.formatMessage({ id: "web.routes.server_settings.security_check_built_in", defaultMessage: "Only when wrong (built-in)", description: "Advanced security: module default text for the volume ownership check when the module sets none; the built-in Gameplane default is Only when wrong" }) })}
      status={status ? <SettingStatus applied={applied?.volumeOwnershipCheck} desired={desired?.volumeOwnershipCheck} /> : undefined}
      chips={
        <>
          {noFsGroup && (
            <Chip size="sm" variant="soft">
              {intl.formatMessage({ id: "web.routes.server_settings.security_no_fs_group", defaultMessage: "No effect: no filesystem group", description: "Advanced security: chip on the volume ownership check when no filesystem group applies, so the check does nothing" })}
            </Chip>
          )}
          {risky?.volumeOwnershipCheck !== undefined && checkOverride !== undefined && (
            <Chip size="sm" variant="soft" color="warning">
              {intl.formatMessage({ id: "web.routes.server_settings.security_risky", defaultMessage: "Risky", description: "Advanced security: warning chip on a setting the module marks as risky when the owner has overridden it" })}
            </Chip>
          )}
        </>
      }
    >
      <OwnershipControls
        label={labels.volumeOwnershipCheck}
        value={checkOverride}
        readOnly={readOnly}
        onPropose={(next) => propose("volumeOwnershipCheck", next)}
      />
    </SettingRow>
  );

  return (
    <div className="space-y-4 rounded-2xl border border-border bg-surface p-5">
      <div className="space-y-2">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={panelId}
          onClick={() => {
            if (open) setRowInvalid({});
            setOpen(!open);
          }}
          className="flex w-full flex-wrap items-center gap-2 text-left"
        >
          {open ? <ChevronDown className="h-4 w-4 shrink-0" aria-hidden="true" /> : <ChevronRight className="h-4 w-4 shrink-0" aria-hidden="true" />}
          <span className="text-sm font-semibold">
            {intl.formatMessage({ id: "web.routes.server_settings.security_advanced_title", defaultMessage: "Advanced security", description: "Advanced security: title of the collapsible security section on the server settings and create-server pages" })}
          </span>
          {count > 0 && (
            <Chip size="sm" variant="soft" color="accent">
              {intl.formatMessage({ id: "web.routes.server_settings.security_override_count", defaultMessage: "{count, plural, one {# override} other {# overrides}}", description: "Advanced security: chip with the number of settings the owner has overridden; {count} is the number" }, { count })}
            </Chip>
          )}
          {readOnly && (
            <Chip size="sm" variant="soft">
              {intl.formatMessage({ id: "web.routes.server_settings.security_read_only", defaultMessage: "Read-only", description: "Advanced security: chip shown when the user can see the security settings but not change them" })}
            </Chip>
          )}
        </button>
        <p className="text-sm text-muted">
          {intl.formatMessage({ id: "web.routes.server_settings.security_description", defaultMessage: "Who the game runs as, and how the data volume's group is fixed. Leave a setting on Module default unless the game needs something else.", description: "Advanced security: description under the Advanced security title" })}
        </p>
      </div>

      <div id={panelId} className="space-y-4">
        {open && (
          <>
            {heldBack !== undefined && (
              <Alert status="warning" className="flex items-start gap-3">
                <Alert.Indicator className="mt-0.5 shrink-0">
                  <TriangleAlert className="h-5 w-5" aria-hidden="true" />
                </Alert.Indicator>
                <Alert.Content>
                  <Alert.Title>
                    {heldBack.running
                      ? intl.formatMessage({ id: "web.routes.server_settings.security_held_back_running_title", defaultMessage: "Keeps running, but will not start again", description: "Advanced security: warning title when a server with a root override is running but cannot start again because root overrides are off" })
                      : intl.formatMessage({ id: "web.routes.server_settings.security_held_back_stopped_title", defaultMessage: "Will not start: root override not allowed", description: "Advanced security: warning title when a stopped server has a root override that the cluster does not allow" })}
                  </Alert.Title>
                  <Alert.Description>
                    {heldBack.message ?? (heldBack.running
                      ? intl.formatMessage({ id: "web.routes.server_settings.security_held_back_running_body", defaultMessage: "A root override is set, and root overrides are off on this cluster. The server keeps running. It will not start again until the override is cleared or an administrator allows root overrides in that cluster's Configuration.", description: "Advanced security: default warning body when a running server holds a root override the cluster does not allow" })
                      : intl.formatMessage({ id: "web.routes.server_settings.security_held_back_stopped_body", defaultMessage: "A root override is set, and root overrides are off on this cluster. Clear the override, or ask an administrator to allow root overrides in that cluster's Configuration.", description: "Advanced security: default warning body when a stopped server holds a root override the cluster does not allow" }))}
                  </Alert.Description>
                </Alert.Content>
              </Alert>
            )}
            {status?.pending === true && (
              <div data-testid="security-restart-note">
                <Alert status="accent" className="flex items-start gap-3">
                  <Alert.Content>
                    <Alert.Title>
                      {intl.formatMessage({ id: "web.routes.server_settings.security_restart_title", defaultMessage: "Applies on the next restart", description: "Advanced security: accent notice title saying saved security values take effect when the server next starts" })}
                    </Alert.Title>
                    <Alert.Description>
                      {intl.formatMessage({ id: "web.routes.server_settings.security_restart_body", defaultMessage: "Saving never restarts the server. Pending values take effect the next time it starts.", description: "Advanced security: notice body saying saving does not restart the server and pending values apply on the next start" })}
                    </Alert.Description>
                  </Alert.Content>
                </Alert>
              </div>
            )}
            {readOnly && (
              <p className="text-xs text-muted">
                {intl.formatMessage({ id: "web.routes.server_settings.security_read_only_note", defaultMessage: "You can see these settings. Changing them needs the servers:security permission.", description: "Advanced security: note shown to users who can see but not change the security settings; servers:security is a permission name, keep it unchanged" })}
              </p>
            )}
            {ID_SETTINGS.map((s) => idRow(s))}
            {ownershipRow}
          </>
        )}
      </div>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(next) => {
          if (!next) cancelRisk();
        }}
        title={pending !== null
          ? intl.formatMessage({ id: "web.routes.server_settings.security_risky_title", defaultMessage: "Keep a risky value for {setting}?", description: "Advanced security: confirmation dialog title before keeping a value the module marks as risky; {setting} is the setting name, such as Run-as user" }, { setting: labels[pending.setting] })
          : ""}
        description={pending !== null ? (
          <div className="space-y-3">
            <p>{pending.reason !== undefined
              ? intl.formatMessage({ id: "web.routes.server_settings.security_risky_intro_reason", defaultMessage: "The module marks this setting as risky. Its reason:", description: "Advanced security: confirmation dialog sentence before the module's plain-text reason for marking a setting risky" })
              : intl.formatMessage({ id: "web.routes.server_settings.security_risky_intro", defaultMessage: "The module marks this setting as risky.", description: "Advanced security: confirmation dialog sentence saying the module marks the setting as risky, used when the module gives no reason" })}
            </p>
            {pending.reason !== undefined && <p className="rounded-xl bg-surface-secondary p-3 text-sm">{pending.reason}</p>}
            <p>{intl.formatMessage({ id: "web.routes.server_settings.security_risky_outro", defaultMessage: "The server may fail to start with this value. You can clear the override later to go back to the module default.", description: "Advanced security: confirmation dialog sentence warning the server may fail to start and how to undo the override" })}</p>
          </div>
        ) : null}
        confirmLabel={intl.formatMessage({ id: "web.routes.server_settings.security_risky_confirm", defaultMessage: "Keep value", description: "Advanced security: confirm button label in the risky value dialog, keeping the value the owner entered" })}
        onConfirm={confirmRisk}
      />
    </div>
  );
}
