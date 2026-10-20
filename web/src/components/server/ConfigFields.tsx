import { useId, type ReactNode } from "react";
import { Button, Input } from "@heroui/react";

import { cn } from "@/lib/utils";
import { CONFIG_REDACTED_MARKER, type ConfigField } from "@/lib/validation";
import { useOptionalIntl } from "@/lib/i18n/format";
import { identityText, moduleIds, type ModuleText } from "@/lib/i18n/moduleText";

// ConfigFields renders a template's configSchema as form fields. It is the
// single implementation shared by the Create wizard (variant "wizard", the
// original stacked layout) and Settings > Game configuration (variant
// "settings": label-left rows, required marker, write-only passwords, file
// note, inline errors). No per-game branching: everything comes from the
// schema.

export type ConfigFieldVariant = "wizard" | "settings";

// The constants below are the English reference for messages rendered through
// intl. Each must stay equal to the defaultMessage of its intl descriptor; the
// tests import them.
export const STORED_PASSWORD_PLACEHOLDER = "Unchanged — type to replace";
export const REMOVING_PASSWORD_PLACEHOLDER = "Password will be removed";
export const REMOVING_PASSWORD_NOTE = "Removed when you save. Undo to keep the stored password.";
// Leading empty <option> labels for selects whose draft value is empty: unset
// (no value, no default) or reported by the API as the redaction marker.
export const UNSET_OPTION_LABEL = "Select…";
export const UNCHANGED_OPTION_LABEL = "Unchanged";

const SELECT_BASE = "h-9 w-full rounded-md border border-border bg-surface px-3 text-sm";

export interface ConfigFieldInputProps {
  field: ConfigField;
  /** Raw spec.config value; undefined when the key is unset. */
  value: string | undefined;
  onChange: (value: string) => void;
  variant?: ConfigFieldVariant;
  disabled?: boolean;
  /** Module-translated text accessor; the template's own text when omitted. */
  text?: ModuleText;
  /** Inline error text (rendered by the settings variant only). */
  error?: string;
  /**
   * Settings variant: the stored optional password is marked for removal. The
   * control shows "will be removed" and an Undo button instead of Remove.
   */
  removing?: boolean;
  /** Settings variant: offered for a stored optional password. */
  onRemove?: () => void;
  onUndo?: () => void;
}

export function ConfigFieldInput({
  field,
  value,
  onChange,
  variant = "wizard",
  disabled = false,
  error,
  removing = false,
  onRemove,
  onUndo,
  text = identityText,
}: ConfigFieldInputProps) {
  const intl = useOptionalIntl();
  const id = useId();
  const errorId = `${id}-error`;
  const noteId = `${id}-note`;
  const labelId = `${id}-label`;
  const label = text(moduleIds.configDisplayName(field.name), field.displayName ?? field.name);
  const settings = variant === "settings";
  const invalid = settings && error !== undefined && error !== "";
  // While the API reports a stored value (the redaction marker: always for a
  // password, and for every key when the API cannot read the template and
  // redacts everything) the control is shown empty/"Unchanged" and the marker
  // is never rendered, whatever the field type or variant.
  const stored = value === CONFIG_REDACTED_MARKER;

  const common = {
    id,
    disabled,
    "aria-invalid": invalid ? true : undefined,
    "aria-describedby": [invalid ? errorId : "", removing ? noteId : ""].filter(Boolean).join(" ") || undefined,
  } as const;

  let control: ReactNode;
  if (field.type === "enum") {
    // The select always shows exactly what the draft holds: with no value and
    // no default (or the marker) that is a leading empty option, not the first
    // enum member.
    const current = stored ? "" : (value ?? field.default ?? "");
    control = (
      <select
        {...common}
        className={cn(SELECT_BASE, invalid && "border-danger", disabled && "opacity-60")}
        value={current}
        onChange={(e) => onChange(e.target.value)}
      >
        {current === "" && (
          <option value="">{stored ? intl.formatMessage({ id: "web.components.server.config_fields.option_unchanged", defaultMessage: "Unchanged", description: "Config fields: option label of an empty select whose stored value is kept as unchanged" }) : intl.formatMessage({ id: "web.components.server.config_fields.option_select", defaultMessage: "Select…", description: "Config fields: option label of an empty select with no value and no default, shown until a value is picked" })}</option>
        )}
        {field.enum?.map((v) => (
          <option key={v} value={v}>
            {text(moduleIds.configOption(field.name, v), v)}
          </option>
        ))}
      </select>
    );
  } else if (field.type === "bool") {
    // An optional bool with no value/default reads "false" (unset is valid). A
    // REQUIRED one has no implicit value: show the empty option so Save stays
    // disabled until a real value is chosen (and choosing "false" fires onChange).
    const current = stored ? "" : (value ?? field.default ?? (field.required ? "" : "false"));
    control = (
      <select
        {...common}
        className={cn(SELECT_BASE, invalid && "border-danger", disabled && "opacity-60")}
        value={current}
        onChange={(e) => onChange(e.target.value)}
      >
        {current === "" && (
          <option value="">{stored ? intl.formatMessage({ id: "web.components.server.config_fields.option_unchanged", defaultMessage: "Unchanged", description: "Config fields: option label of an empty select whose stored value is kept as unchanged" }) : intl.formatMessage({ id: "web.components.server.config_fields.option_select", defaultMessage: "Select…", description: "Config fields: option label of an empty select with no value and no default, shown until a value is picked" })}</option>
        )}
        <option value="true">{intl.formatMessage({ id: "web.components.server.config_fields.true", defaultMessage: "true", description: "Config fields: dropdown option meaning the setting is on, the value true; keep it short" })}</option>
        <option value="false">{intl.formatMessage({ id: "web.components.server.config_fields.false", defaultMessage: "false", description: "Config fields: dropdown option meaning the setting is off, the value false; keep it short" })}</option>
      </select>
    );
  } else {
    const shown = stored || removing ? "" : (value ?? field.default ?? "");
    const placeholder = removing
      ? intl.formatMessage({ id: "web.components.server.config_fields.password_will_be_removed", defaultMessage: "Password will be removed", description: "Config fields: placeholder of a stored password field that is marked for removal" })
      : stored
        ? intl.formatMessage({ id: "web.components.server.config_fields.unchanged_type_to_replace", defaultMessage: "Unchanged — type to replace", description: "Config fields: placeholder of a stored password field; typing replaces the stored value" })
        : field.autoFromMemoryLimit
        ? intl.formatMessage({ id: "web.components.server.config_fields.auto_of_memory_limit", defaultMessage: "Auto: {percent}% of the memory limit", description: "Config fields: placeholder of a field derived from the memory limit; {percent} is the percentage of the limit" }, { percent: String(field.autoFromMemoryLimit.percent) })
        : undefined;
    control = (
      <Input
        {...common}
        type={field.type === "password" ? "password" : "text"}
        inputMode={field.type === "int" ? "numeric" : undefined}
        className={invalid ? "border-danger" : removing ? "border-danger placeholder:text-danger" : undefined}
        value={shown}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  let removeAction: ReactNode = null;
  if (settings && removing && onUndo && !disabled) {
    removeAction = (
      <Button
        variant="ghost"
        size="sm"
        aria-describedby={labelId}
        onPress={onUndo}
      >{intl.formatMessage({ id: "web.components.server.config_fields.undo", defaultMessage: "Undo", description: "Config fields: button under a password field that cancels the pending removal of the stored password; keep it short" })}</Button>
    );
  } else if (settings && stored && field.type === "password" && !field.required && onRemove && !disabled) {
    removeAction = (
      <Button
        variant="ghost"
        size="sm"
        aria-describedby={labelId}
        onPress={onRemove}
      >{intl.formatMessage({ id: "web.components.server.config_fields.remove_password", defaultMessage: "Remove password", description: "Config fields: button under a stored password field that marks the password for removal; keep it short" })}</Button>
    );
  }

  if (!settings) {
    return (
      <label className="space-y-1.5 block">
        <div className="text-xs text-muted">{label}</div>
        {control}
        {field.description && <span className="text-[11px] text-muted">{text(moduleIds.configDescription(field.name), field.description)}</span>}
      </label>
    );
  }

  return (
    <div className="grid grid-cols-1 items-start gap-1.5 sm:grid-cols-[200px_1fr] sm:gap-4">
      <label id={labelId} htmlFor={id} className="text-sm text-fg sm:pt-2">
        {label}
        {field.required && (
          <span aria-hidden="true" className="text-danger">
            {intl.formatMessage({ id: "web.common.required_marker", defaultMessage: " *", description: "Asterisk shown after the label of a required field" })}
          </span>
        )}
      </label>
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <div className="flex-1">{control}</div>
          {removeAction}
        </div>
        {field.description && <p className="text-xs text-muted">{text(moduleIds.configDescription(field.name), field.description)}</p>}
        {field.type === "password" &&
          (removing ? (
            <p id={noteId} className="text-xs text-danger">
              {intl.formatMessage({ id: "web.components.server.config_fields.removing_password_note", defaultMessage: "Removed when you save. Undo to keep the stored password.", description: "Config fields: note under a stored password field marked for removal; explains that the stored value is removed on save and Undo keeps it" })}
            </p>
          ) : (
            <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.server.config_fields.write_only", defaultMessage: "Write-only", description: "Config fields: small note under a password field saying the stored value is write-only and cannot be shown again" })}</p>
          ))}
        {field.target === "file" && <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.server.config_fields.written_to_file", defaultMessage: "Written to file", description: "Config fields: small note under a setting saying its value is written to a file rather than set directly" })}</p>}
        {invalid && (
          <p id={errorId} role="alert" className="text-xs text-danger">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}

export interface ConfigFieldsProps {
  schema: ConfigField[];
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
  variant?: ConfigFieldVariant;
  disabled?: boolean;
  /** Module-translated text accessor; the template's own text when omitted. */
  text?: ModuleText;
  errors?: Record<string, string>;
  /** Settings variant: names of stored optional passwords marked for removal. */
  removing?: ReadonlySet<string>;
  onRemove?: (name: string) => void;
  onUndo?: (name: string) => void;
}

export function ConfigFields({
  schema,
  values,
  onChange,
  variant = "wizard",
  disabled = false,
  text = identityText,
  errors,
  removing,
  onRemove,
  onUndo,
}: ConfigFieldsProps) {
  return (
    <>
      {schema.map((f) => (
        <ConfigFieldInput
          key={f.name}
          field={f}
          value={values[f.name]}
          onChange={(v) => onChange(f.name, v)}
          variant={variant}
          disabled={disabled}
          text={text}
          error={errors?.[f.name]}
          removing={removing?.has(f.name)}
          onRemove={onRemove && (() => onRemove(f.name))}
          onUndo={onUndo && (() => onUndo(f.name))}
        />
      ))}
    </>
  );
}
