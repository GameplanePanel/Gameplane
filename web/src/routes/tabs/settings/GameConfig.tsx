import { useEffect, useMemo, useRef } from "react";
import { AlertCircle, AlertTriangle } from "lucide-react";
import { Alert, Button, Input } from "@heroui/react";

import { ConfigFields } from "@/components/server/ConfigFields";
import { useResourceAccess } from "@/lib/resourceTarget";
import { CONFIG_REDACTED_MARKER, validateConfig } from "@/lib/validation";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";

// The operator reports a rejected spec.config as a Ready=False condition whose
// message starts with this prefix (gameserver_controller.go).
const INVALID_CONFIG_PREFIX = "invalid config:";

// GameConfigSection edits spec.config from the template's configSchema. Unset
// key = template default (or auto). Only non-default values are written, so a
// later template default change still flows. Password values are write-only:
// the API returns a marker for a stored password (or for any key it cannot classify); the marker stays in the
// draft until the user types, and the PUT then keeps the stored value. An
// optional stored password is cleared only through the explicit Remove button
// (spec 021 OD-2); emptying a typed value still returns to "unchanged".
export function GameConfigSection({ draft, onChange, template, moduleText, storedConfig, onValidityChange }: SectionProps) {
  const intl = useOptionalIntl();
  const access = useResourceAccess();
  const canWrite = access?.canWrite === true;
  const schema = useMemo(() => template?.spec.configSchema ?? [], [template]);
  const values = useMemo(() => draft.spec.config ?? {}, [draft.spec.config]);

  const errors = useMemo(() => validateConfig(schema, values, intl), [schema, values, intl]);
  const errorText = useMemo(
    () => Object.fromEntries(errors.map((e) => [e.name, e.text])),
    [errors],
  );
  useEffect(() => {
    onValidityChange?.(errors.length === 0);
  }, [errors, onValidityChange]);

  // Names of fields the API has reported as the redaction marker (stored
  // passwords, or every key when the API could not read the template).
  // Emptying an input the user had started typing in returns to "unchanged".
  const storedRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    for (const f of schema) {
      if (values[f.name] === CONFIG_REDACTED_MARKER) {
        storedRef.current.add(f.name);
      }
    }
  }, [schema, values]);

  // Stored optional passwords marked for removal: the saved server holds the
  // marker but the draft has no key (the API deletes an optional password when
  // its key is missing). Derived, so it survives switching Settings sections;
  // Discard and a successful save change the saved config or the draft.
  const removing = useMemo(
    () =>
      new Set(
        schema
          .filter(
            (f) =>
              f.type === "password" &&
              !f.required &&
              storedConfig?.[f.name] === CONFIG_REDACTED_MARKER &&
              values[f.name] === undefined,
          )
          .map((f) => f.name),
      ),
    [schema, storedConfig, values],
  );

  const known = useMemo(() => new Set(schema.map((f) => f.name)), [schema]);
  const orphans = useMemo(
    () => Object.keys(values).filter((k) => !known.has(k)).sort(),
    [values, known],
  );

  const commit = (next: Record<string, string>) => {
    onChange({
      ...draft,
      spec: { ...draft.spec, config: Object.keys(next).length > 0 ? next : undefined },
    });
  };

  const setValue = (name: string, value: string) => {
    const field = schema.find((f) => f.name === name);
    const next = { ...values };
    if (value === "" && (storedRef.current.has(name) || storedConfig?.[name] === CONFIG_REDACTED_MARKER)) {
      // Emptied a field the API reported as the marker: back to "unchanged".
      // The saved config also counts, because a remounted section (after a
      // Remove and a section switch) never saw the marker in its draft.
      next[name] = CONFIG_REDACTED_MARKER;
    } else if (field?.type === "password" && value === "") {
      delete next[name];
    } else if (value === (field?.default ?? "")) {
      // Equal to the template default (or empty with no default): unset.
      delete next[name];
    } else {
      next[name] = value;
    }
    commit(next);
  };

  const removePassword = (name: string) => {
    const next = { ...values };
    delete next[name];
    commit(next);
  };

  const undoRemovePassword = (name: string) => {
    // Put the key back where the saved config had it: Settings compares the
    // draft to the baseline by JSON, so key order decides "dirty".
    const next: Record<string, string> = {};
    for (const key of Object.keys(storedConfig ?? {})) {
      if (key === name) next[key] = CONFIG_REDACTED_MARKER;
      else if (key in values) next[key] = values[key];
    }
    for (const key of Object.keys(values)) {
      if (!(key in next)) next[key] = values[key];
    }
    next[name] ??= CONFIG_REDACTED_MARKER;
    commit(next);
  };

  const removeOrphan = (name: string) => {
    const next = { ...values };
    delete next[name];
    commit(next);
  };

  const ready = draft.status?.conditions?.find((c) => c.type === "Ready");
  const invalidMessage = ready?.message?.startsWith(INVALID_CONFIG_PREFIX) ? ready.message : null;

  return (
    <div className="max-w-3xl space-y-5">
      <div>
        <h2 className="text-lg font-semibold">{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.game_configuration", defaultMessage: "Game configuration", description: "Game config: heading of the game configuration section, shown at the top of the section" })}</h2>
        <p className="pt-1 text-[13px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.settings_from_this_server_s_game", defaultMessage: "Settings from this server's game template. Saving restarts the server.", description: "Game config: hint under the heading saying these settings come from the game template and that saving restarts the server" })}</p>
      </div>

      {invalidMessage && (
        <Alert status="danger" data-testid="config-invalid">
          <Alert.Indicator>
            <AlertCircle className="h-5 w-5 shrink-0" />
          </Alert.Indicator>
          <Alert.Content>
            <div className="font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.the_operator_rejected_this_configuration", defaultMessage: "The operator rejected this configuration", description: "Game config: title of the danger alert shown when the operator rejects the configuration; the detail line below gives the reason" })}</div>
            <div className="pt-1 text-xs">{invalidMessage}</div>
          </Alert.Content>
        </Alert>
      )}

      {canWrite && (
        <Alert status="warning" data-testid="config-restart-note">
          <Alert.Indicator>
            <AlertTriangle className="h-5 w-5 shrink-0" />
          </Alert.Indicator>
          <Alert.Content>{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.saving_changes_restarts_the_server_so", defaultMessage: "Saving changes restarts the server so they apply.", description: "Game config: warning note saying that saving changes restarts the server so they apply; shown to users who can write the settings" })}</Alert.Content>
        </Alert>
      )}

      {schema.length === 0 && orphans.length === 0 && (
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.this_template_has_no_configurable", defaultMessage: "This template has no configurable settings.", description: "Game config: empty-state text shown when the game template has no configurable settings" })}</p>
      )}

      <div className="space-y-5">
        <ConfigFields
          schema={schema}
          values={values}
          onChange={setValue}
          variant="settings"
          disabled={!canWrite}
          text={moduleText}
          errors={errorText}
          removing={removing}
          onRemove={removePassword}
          onUndo={undoRemovePassword}
        />

        {orphans.map((key) => (
          <div
            key={key}
            className="grid grid-cols-1 items-start gap-1.5 sm:grid-cols-[200px_1fr] sm:gap-4"
          >
            <div className="font-mono text-sm text-fg sm:pt-2">{key}</div>
            <div className="space-y-1">
              <div className="flex items-center gap-2">
                <Input
                  aria-label={key}
                  readOnly
                  value={values[key] === CONFIG_REDACTED_MARKER ? "" : values[key]}
                  placeholder={values[key] === CONFIG_REDACTED_MARKER ? intl.formatMessage({ id: "web.routes.tabs.settings.gameconfig.hidden", defaultMessage: "Hidden", description: "GameConfig: placeholder of a redacted config value input that is hidden from view" }) : undefined}
                  className="flex-1"
                />
                <Button
                  variant="ghost"
                  size="sm"
                  onPress={() => removeOrphan(key)}
                  isDisabled={!canWrite}
                >{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.remove", defaultMessage: "Remove", description: "Button that removes this config value override" })}</Button>
              </div>
              <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.game_config.no_longer_in_the_template", defaultMessage: "No longer in the template", description: "Game config: note on a saved setting that the game template no longer offers; shown next to a Remove button" })}</p>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
