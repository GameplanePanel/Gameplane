import { defineMessages, type IntlShape } from "react-intl";

import { englishIntl } from "@/lib/i18n/format";
import type { GameTemplate } from "@/types";

// Descriptors with placeholders are passed inline to formatMessage (typed values);
// descriptors without placeholders live in defineMessages.
const MESSAGES = defineMessages({
  whole_number: {
    id: "web.lib.validation.whole_number",
    defaultMessage: "Must be a whole number.",
    description: "Config field validation: integer field value is not a whole number",
  },
  true_false: {
    id: "web.lib.validation.true_false",
    defaultMessage: "Must be true or false.",
    description: "Config field validation: boolean field value is neither true nor false",
  },
});

export type ConfigField = NonNullable<GameTemplate["spec"]["configSchema"]>[number];

const DNS_LABEL = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const QUANTITY = /^(\d+)(\.\d+)?(Ki|Mi|Gi|Ti|Pi|Ei|m|k|M|G|T|P|E)?$/;

export function isValidK8sName(s: string): boolean {
  return s.length > 0 && s.length <= 63 && DNS_LABEL.test(s);
}

export function isValidQuantity(s: string): boolean {
  const trimmed = s.trim();
  if (trimmed === "" || trimmed !== s) return false;
  return QUANTITY.test(trimmed);
}

// isValidVersion checks a chosen version id against a template's catalog.
// When the template declares no versions, the version is irrelevant (true).
// When it does, a value must be supplied and must match a catalog id.
export function isValidVersion(
  template: GameTemplate | undefined,
  version: string | undefined,
): boolean {
  const versions = template?.spec.versions;
  if (!versions || versions.length === 0) return true;
  if (!version) return false;
  return versions.some((v) => v.id === version);
}

// defaultVersionId returns the template's pre-selected version: the entry
// marked default, else the first, else undefined when no catalog exists.
export function defaultVersionId(template: GameTemplate | undefined): string | undefined {
  const versions = template?.spec.versions;
  if (!versions || versions.length === 0) return undefined;
  return (versions.find((v) => v.default) ?? versions[0]).id;
}

export interface ConfigError {
  name: string;
  // Full sentence for step-level summaries (the wizard shows the first one).
  message: string;
  // Text for the inline message under the field (no field name prefix).
  text: string;
}

// CONFIG_REDACTED_MARKER is what the API sends instead of a stored
// password-type spec.config value (or of any value when it cannot read the template). Sending it back in a PUT keeps the stored
// value. It must never be rendered.
export const CONFIG_REDACTED_MARKER = "__gameplane_redacted__";

const GO_BOOLS = ["1", "t", "T", "TRUE", "true", "True", "0", "f", "F", "FALSE", "false", "False"];

// fieldValueProblem mirrors the operator's materializeConfig checks for one
// non-empty value. The operator stays authoritative; this only gives early
// feedback.
function fieldValueProblem(field: ConfigField, value: string, intl: IntlShape): string | null {
  switch (field.type) {
    case "int": {
      if (!/^[+-]?\d+$/.test(value)) return intl.formatMessage(MESSAGES.whole_number);
      const n = Number(value);
      const { min, max } = field;
      // Bounds are passed as strings so English keeps the exact digits (no grouping separators).
      if (min != null && max != null && (n < min || n > max)) return intl.formatMessage({
        id: "web.lib.validation.between",
        defaultMessage: "Must be between {min} and {max}.",
        description: "Config field validation: number outside a min and max range; {min} and {max} are the limits",
      }, { min: String(min), max: String(max) });
      if (min != null && n < min) return intl.formatMessage({
        id: "web.lib.validation.at_least",
        defaultMessage: "Must be at least {min}.",
        description: "Config field validation: number below the minimum; {min} is the minimum",
      }, { min: String(min) });
      if (max != null && n > max) return intl.formatMessage({
        id: "web.lib.validation.at_most",
        defaultMessage: "Must be at most {max}.",
        description: "Config field validation: number above the maximum; {max} is the maximum",
      }, { max: String(max) });
      return null;
    }
    case "bool":
      return GO_BOOLS.includes(value) ? null : intl.formatMessage(MESSAGES.true_false);
    case "string":
    case "password": {
      const len = new TextEncoder().encode(value).length;
      const { minLength, maxLength } = field;
      if (minLength != null && len < minLength) return intl.formatMessage({
        id: "web.lib.validation.chars_at_least",
        defaultMessage: "Must be at least {min} characters.",
        description: "Config field validation: text shorter than the minimum length; {min} is the minimum number of characters",
      }, { min: String(minLength) });
      if (maxLength != null && len > maxLength) return intl.formatMessage({
        id: "web.lib.validation.chars_at_most",
        defaultMessage: "Must be at most {max} characters.",
        description: "Config field validation: text longer than the maximum length; {max} is the maximum number of characters",
      }, { max: String(maxLength) });
      return null;
    }
    default:
      return null;
  }
}

export function validateConfig(
  schema: ConfigField[],
  values: Record<string, string>,
  intl: IntlShape = englishIntl,
): ConfigError[] {
  const errors: ConfigError[] = [];
  for (const field of schema) {
    const raw = values[field.name];
    const provided = raw ?? field.default ?? "";
    const label = field.displayName ?? field.name;
    if (field.required && provided === "") {
      const message = intl.formatMessage({
        id: "web.lib.validation.required",
        defaultMessage: "{label} is required",
        description: "Config field validation: shown under a required config field left empty; {label} is the field's display name",
      }, { label });
      errors.push({ name: field.name, message, text: message });
      continue;
    }
    if (provided === "") continue;
    // A value the API reported as the redaction marker is unchanged, whatever
    // the field type: nothing to check.
    if (provided === CONFIG_REDACTED_MARKER) continue;
    if (field.type === "enum" && field.enum && !field.enum.includes(provided)) {
      const message = intl.formatMessage({
        id: "web.lib.validation.one_of",
        defaultMessage: "{label} must be one of: {options}",
        description: "Config field validation: enum field value not in the allowed list; {label} is the field's display name and {options} is a comma-separated list of allowed values",
      }, { label, options: field.enum.join(", ") });
      errors.push({ name: field.name, message, text: message });
      continue;
    }
    const problem = fieldValueProblem(field, provided, intl);
    if (problem) {
      const message = intl.formatMessage({
        id: "web.lib.validation.label_problem",
        defaultMessage: "{label}: {problem}",
        description: "Config field validation: prefixes a value problem with the field name; {label} is the field's display name and {problem} is the problem sentence",
      }, { label, problem });
      errors.push({ name: field.name, message, text: problem });
    }
  }
  return errors;
}

// PASSWORD_MASK is shown in place of a password-type template config value
// anywhere config values are displayed. The API itself never returns stored
// passwords (it sends a fixed redaction marker), so the dashboard must not
// render either the value or the marker.
export const PASSWORD_MASK = "********";

// maskPasswordConfig returns a copy of values with every non-empty value of a
// password-type field replaced by PASSWORD_MASK. Empty values stay empty.
export function maskPasswordConfig(
  schema: ConfigField[],
  values: Record<string, string>,
): Record<string, string> {
  const passwords = new Set(schema.filter((f) => f.type === "password").map((f) => f.name));
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(values)) {
    out[name] = passwords.has(name) && value !== "" ? PASSWORD_MASK : value;
  }
  return out;
}
