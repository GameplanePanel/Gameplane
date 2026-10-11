import { useEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, AlertTriangle } from "lucide-react";
import { Alert, Button, Input } from "@heroui/react";

import { ConfigFields } from "@/components/server/ConfigFields";
import { useResourceAccess } from "@/lib/resourceTarget";
import { CONFIG_REDACTED_MARKER, validateConfig } from "@/lib/validation";
import type { SectionProps } from "./types";

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
export function GameConfigSection({ draft, onChange, template, onValidityChange }: SectionProps) {
  const access = useResourceAccess();
  const canWrite = access?.canWrite === true;
  const schema = useMemo(() => template?.spec.configSchema ?? [], [template]);
  const values = useMemo(() => draft.spec.config ?? {}, [draft.spec.config]);

  const errors = useMemo(() => validateConfig(schema, values), [schema, values]);
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

  // Stored optional passwords the user marked for removal. The key is absent
  // from the draft (the API deletes an optional password when its key is
  // missing); this set only drives the "will be removed" state. The section
  // remounts on save/discard (Settings draftRevision), which resets it.
  const [removing, setRemoving] = useState<ReadonlySet<string>>(new Set());

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
    if (removing.has(name)) {
      if (value === "") return;
      setRemoving((prev) => {
        const rest = new Set(prev);
        rest.delete(name);
        return rest;
      });
    }
    if (value === "" && storedRef.current.has(name)) {
      // Emptied a field the API reported as the marker: back to "unchanged".
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
    setRemoving((prev) => new Set(prev).add(name));
    commit(next);
  };

  const undoRemovePassword = (name: string) => {
    setRemoving((prev) => {
      const rest = new Set(prev);
      rest.delete(name);
      return rest;
    });
    commit({ ...values, [name]: CONFIG_REDACTED_MARKER });
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
        <h2 className="text-lg font-semibold">Game configuration</h2>
        <p className="pt-1 text-[13px] text-muted">
          Settings from this server&apos;s game template. Saving restarts the server.
        </p>
      </div>

      {invalidMessage && (
        <Alert status="danger" data-testid="config-invalid">
          <Alert.Indicator>
            <AlertCircle className="h-5 w-5 shrink-0" />
          </Alert.Indicator>
          <Alert.Content>
            <div className="font-medium">The operator rejected this configuration</div>
            <div className="pt-1 text-xs">{invalidMessage}</div>
          </Alert.Content>
        </Alert>
      )}

      {canWrite && (
        <Alert status="warning" data-testid="config-restart-note">
          <Alert.Indicator>
            <AlertTriangle className="h-5 w-5 shrink-0" />
          </Alert.Indicator>
          <Alert.Content>Saving changes restarts the server so they apply.</Alert.Content>
        </Alert>
      )}

      {schema.length === 0 && orphans.length === 0 && (
        <p className="text-sm text-muted">This template has no configurable settings.</p>
      )}

      <div className="space-y-5">
        <ConfigFields
          schema={schema}
          values={values}
          onChange={setValue}
          variant="settings"
          disabled={!canWrite}
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
                  placeholder={values[key] === CONFIG_REDACTED_MARKER ? "Hidden" : undefined}
                  className="flex-1"
                />
                <Button
                  variant="ghost"
                  size="sm"
                  onPress={() => removeOrphan(key)}
                  isDisabled={!canWrite}
                >
                  Remove
                </Button>
              </div>
              <p className="text-xs text-muted">No longer in the template</p>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
