import { Input, Button } from "@heroui/react";
import { X } from "lucide-react";
import type { PortOverride } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

// T083: Row editor for GameServer.spec.networking.portOverrides — shared between
// the server Settings → Networking tab and the Create Server wizard.
export function PortOverridesEditor({
  values,
  onChange,
}: {
  values: PortOverride[];
  onChange: (v: PortOverride[]) => void;
}) {
  const intl = useOptionalIntl();
  const update = (idx: number, patch: Partial<PortOverride>) => {
    onChange(values.map((p, i) => (i === idx ? { ...p, ...patch } : p)));
  };
  const remove = (idx: number) => {
    onChange(values.filter((_, i) => i !== idx));
  };
  const add = () => {
    onChange([...values, { name: "" }]);
  };
  return (
    <div className="space-y-2">
      {values.length > 0 && (
        <div className="hidden gap-2 text-xs text-muted sm:grid sm:grid-cols-[1fr_120px_120px_32px] sm:items-center">
          <span>{intl.formatMessage({ id: "web.components.server.port_overrides_editor.port_name", defaultMessage: "Port name", description: "Port overrides editor: column heading for the port name in the list of port overrides; keep it short" })}</span>
          <span>{intl.formatMessage({ id: "web.components.server.port_overrides_editor.service_port", defaultMessage: "Service port", description: "Port overrides editor: column heading for the service port number in the list of port overrides; keep it short" })}</span>
          <span>{intl.formatMessage({ id: "web.components.server.port_overrides_editor.nodeport", defaultMessage: "NodePort", description: "Port overrides editor: column heading for the NodePort number in the list of port overrides; NodePort is a technical term, usually left untranslated; keep it short" })}</span>
          <span />
        </div>
      )}
      {values.map((p, idx) => (
        <div
          key={idx}
          className="grid grid-cols-1 gap-2 rounded border border-border bg-surface/30 p-2 sm:grid-cols-[1fr_120px_120px_32px] sm:items-center sm:border-0 sm:bg-transparent sm:p-0"
        >
          <Input
            value={p.name}
            onChange={(e) => update(idx, { name: e.target.value })}
            placeholder={intl.formatMessage({ id: "web.components.server.port_overrides_editor.game", defaultMessage: "game", description: "Port overrides editor: example value shown inside the empty port name field of a row; a sample port name, not a default value" })}
          />
          <Input
            value={p.servicePort?.toString() ?? ""}
            onChange={(e) =>
              update(idx, {
                servicePort: e.target.value ? Number(e.target.value) : undefined,
              })
            }
            inputMode="numeric"
            placeholder={intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
          />
          <Input
            value={p.nodePort?.toString() ?? ""}
            onChange={(e) =>
              update(idx, {
                nodePort: e.target.value ? Number(e.target.value) : undefined,
              })
            }
            inputMode="numeric"
            placeholder={intl.formatMessage({ id: "web.components.server.port_overrides_editor.port_range_placeholder", defaultMessage: "30000-32767", description: "Port overrides editor: placeholder example of a NodePort range" })}
          />
          <Button
            variant="ghost"
            size="sm"
            isIconOnly
            className="justify-self-start sm:justify-self-auto"
            aria-label={intl.formatMessage({ id: "web.components.server.port_overrides_editor.remove", defaultMessage: "Remove", description: "Port overrides editor: icon-only button that removes this override row; its screen-reader name and tooltip read Remove; keep it short" })}
            onPress={() => remove(idx)}
          >
            <span title={intl.formatMessage({ id: "web.components.server.port_overrides_editor.remove", defaultMessage: "Remove", description: "Port overrides editor: icon-only button that removes this override row; its screen-reader name and tooltip read Remove; keep it short" })}>
              <X className="h-4 w-4" />
            </span>
          </Button>
        </div>
      ))}
      <Button size="sm" variant="secondary" onPress={add}>{intl.formatMessage({ id: "web.components.server.port_overrides_editor.add_override", defaultMessage: "Add override", description: "Port overrides editor: button that adds a new port override row; keep it short" })}</Button>
    </div>
  );
}
