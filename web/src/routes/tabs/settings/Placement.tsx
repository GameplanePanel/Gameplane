import { useEffect, useState } from "react";
import Editor from "@monaco-editor/react";
import { Label, Description } from "@heroui/react";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";

interface PlacementSectionProps extends SectionProps {
  onValidityChange?: (valid: boolean) => void;
}

export function PlacementSection({
  draft,
  onChange,
  onValidityChange,
}: PlacementSectionProps) {
  const intl = useOptionalIntl();
  // Initialize raw JSON strings once via useState initializers.
  const [rawTol, setRawTol] = useState(() =>
    JSON.stringify(draft.spec.tolerations ?? [], null, 2),
  );
  const [rawAff, setRawAff] = useState(() =>
    JSON.stringify(draft.spec.affinity ?? {}, null, 2),
  );

  const [tolError, setTolError] = useState<string | null>(null);
  const [affError, setAffError] = useState<string | null>(null);

  // Report validity when errors change.
  useEffect(() => {
    onValidityChange?.(tolError === null && affError === null);
  }, [tolError, affError, onValidityChange]);

  const handleTolChange = (v: string | undefined) => {
    const raw = v ?? "";
    setRawTol(raw);

    if (raw.trim() === "") {
      setTolError(null);
      onChange({
        ...draft,
        spec: { ...draft.spec, tolerations: undefined },
      });
      return;
    }

    try {
      const parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) {
        setTolError(intl.formatMessage({ id: "web.routes.tabs.settings.placement.tolerations_must_be_array", defaultMessage: "Must be a JSON array", description: "Placement: validation error when the tolerations JSON is not an array" }));
        return;
      }
      setTolError(null);
      onChange({
        ...draft,
        spec: {
          ...draft.spec,
          tolerations: parsed.length ? parsed : undefined,
        },
      });
    } catch {
      setTolError(intl.formatMessage({ id: "web.routes.tabs.settings.placement.invalid_json", defaultMessage: "Invalid JSON", description: "Placement: validation error when a tolerations or affinity field is not valid JSON" }));
    }
  };

  const handleAffChange = (v: string | undefined) => {
    const raw = v ?? "";
    setRawAff(raw);

    if (raw.trim() === "") {
      setAffError(null);
      onChange({
        ...draft,
        spec: { ...draft.spec, affinity: undefined },
      });
      return;
    }

    try {
      const parsed = JSON.parse(raw);
      if (typeof parsed !== "object" || Array.isArray(parsed) || parsed === null) {
        setAffError(intl.formatMessage({ id: "web.routes.tabs.settings.placement.affinity_must_be_object", defaultMessage: "Must be a JSON object", description: "Placement: validation error when the affinity JSON is not an object" }));
        return;
      }
      setAffError(null);
      onChange({
        ...draft,
        spec: {
          ...draft.spec,
          affinity: Object.keys(parsed).length ? parsed : undefined,
        },
      });
    } catch {
      setAffError(intl.formatMessage({ id: "web.routes.tabs.settings.placement.invalid_json", defaultMessage: "Invalid JSON", description: "Placement: validation error when a tolerations or affinity field is not valid JSON" }));
    }
  };

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 items-start gap-1.5 sm:grid-cols-[200px_1fr] sm:gap-4">
        <div className="sm:pt-2">
          <Label id="tolerations-label" className="text-sm">{intl.formatMessage({ id: "web.routes.tabs.settings.placement.tolerations", defaultMessage: "Tolerations", description: "Label for the tolerations JSON editor" })}</Label>
          <Description className="pt-1 text-xs">{intl.formatMessage({ id: "web.routes.tabs.settings.placement.pod_tolerations_for_kubernetes_node", defaultMessage: "Pod tolerations for Kubernetes node taints. Array of toleration objects (optional).", description: "Placement: hint under the Tolerations label saying the field takes an optional JSON array of toleration objects for Kubernetes node taints" })}</Description>
        </div>
        <div className="space-y-2">
          <div
            className="rounded border border-border bg-surface/50"
            style={{ height: "180px" }}
          >
            <Editor
              aria-labelledby="tolerations-label"
              theme="vs-dark"
              language="json"
              value={rawTol}
              onChange={handleTolChange}
              options={{
                minimap: { enabled: false },
                fontFamily: "JetBrains Mono",
              }}
            />
          </div>
          {tolError && <div className="pt-1 text-xs text-danger">{tolError}</div>}
        </div>
      </div>

      <div className="grid grid-cols-1 items-start gap-1.5 sm:grid-cols-[200px_1fr] sm:gap-4">
        <div className="sm:pt-2">
          <Label id="affinity-label" className="text-sm">{intl.formatMessage({ id: "web.routes.tabs.settings.placement.affinity", defaultMessage: "Affinity", description: "Label for the affinity JSON editor" })}</Label>
          <Description className="pt-1 text-xs">{intl.formatMessage({ id: "web.routes.tabs.settings.placement.pod_affinity_anti_affinity_and_node", defaultMessage: "Pod affinity/anti-affinity and node affinity constraints. JSON object (optional).", description: "Placement: hint under the Affinity label saying the field takes an optional JSON object with pod and node affinity constraints" })}</Description>
        </div>
        <div className="space-y-2">
          <div
            className="rounded border border-border bg-surface/50"
            style={{ height: "180px" }}
          >
            <Editor
              aria-labelledby="affinity-label"
              theme="vs-dark"
              language="json"
              value={rawAff}
              onChange={handleAffChange}
              options={{
                minimap: { enabled: false },
                fontFamily: "JetBrains Mono",
              }}
            />
          </div>
          {affError && <div className="pt-1 text-xs text-danger">{affError}</div>}
        </div>
      </div>
    </div>
  );
}
