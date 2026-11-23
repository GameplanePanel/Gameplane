import { SecurityAdvanced } from "@/components/SecurityAdvanced";
import { useResourceAccess, useResourcePermissions, resourceCan } from "@/lib/resourceTarget";
import type { GameServer } from "@/types";
import type { SectionProps } from "./types";

/**
 * Server settings, Security section: the per-server security overrides (who the
 * game runs as and how the data volume's group is fixed). The panel itself
 * lives in components/SecurityAdvanced; this wires it to the settings draft.
 */
export function SecuritySection({ draft, onChange, template, onValidityChange }: SectionProps) {
  const access = useResourceAccess();
  const permissions = useResourcePermissions();
  const canSecurity = access?.permissions !== undefined && resourceCan(permissions, "servers:security");

  const condition = draft.status?.conditions?.find(
    (c) => c.type === "SecurityAllowed" && c.status === "False" && c.reason === "RootOverrideDisallowed",
  );
  const heldBack = condition !== undefined
    ? { running: draft.status?.phase === "Running", message: condition.message }
    : undefined;

  return (
    <SecurityAdvanced
      value={draft.spec.security}
      onChange={(next) => onChange({ ...draft, spec: { ...draft.spec, security: next } })}
      template={template}
      status={draft.status?.security}
      heldBack={heldBack}
      readOnly={!canSecurity}
      defaultOpen
      onValidityChange={onValidityChange}
    />
  );
}

/**
 * True when saving would change who the game runs as on a server that has
 * already started: existing files keep their old owner, so the user is warned.
 */
export function needsFileAccessWarning(baseline: GameServer | undefined, draft: GameServer): boolean {
  if (!baseline || baseline.status?.security?.applied === undefined) return false;
  const before = baseline.spec.security;
  const after = draft.spec.security;
  return (
    JSON.stringify(before?.runAsUser ?? null) !== JSON.stringify(after?.runAsUser ?? null) ||
    JSON.stringify(before?.runAsGroup ?? null) !== JSON.stringify(after?.runAsGroup ?? null)
  );
}
