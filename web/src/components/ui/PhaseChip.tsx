import { Chip as HeroChip } from "@heroui/react";
import { defineMessages, type IntlShape } from "react-intl";
import type { ReactNode } from "react";
import { useOptionalIntl } from "@/lib/i18n/format";

// PhaseChip renders a phase/state badge using HeroUI Chip.
// Maps GameServer, Backup, and Restore phases to HeroUI semantic colors.
// Asleep is a derived display state overlaid on Suspended.

type PhaseColor = "default" | "success" | "warning" | "danger";

// Phase → HeroUI Chip color mapping
// Covers GameServer phases (Pending/Starting/Running/Stopping/Stopped/Suspended/Failed)
// and Backup/Restore phases (Pending/Running/Succeeded/Failed/Suspending/Resuming)
const phaseColorMap: Record<string, PhaseColor> = {
  // Running states
  Running: "success",
  Succeeded: "success",
  // In-transition states
  Starting: "warning",
  Stopping: "warning",
  Suspending: "warning",
  Resuming: "warning",
  // Stopped/idle states
  Pending: "default",
  Stopped: "default",
  Suspended: "default",
  // Error state
  Failed: "danger",
};

// Generic Chip wrapper for badge use cases
export function Chip({ color = "default", size = "sm", children, className }: {
  color?: PhaseColor;
  size?: "sm" | "md" | "lg";
  children: ReactNode;
  className?: string;
}) {
  return (
    <HeroChip
      color={color}
      size={size}
      data-color={color}
      data-size={size}
      className={className}
    >
      {children}
    </HeroChip>
  );
}

// Display labels for phase values. The phase value itself (used for color and
// data-phase) is never translated; only the text shown to the user is.
// Unknown phase values fall back to the raw value.
const phaseMessages = defineMessages({
  Pending: { id: "web.components.ui.phase_chip.pending", defaultMessage: "Pending", description: "Phase badge: label for a resource that is waiting to start" },
  Starting: { id: "web.components.ui.phase_chip.starting", defaultMessage: "Starting", description: "Phase badge: label for a server that is starting" },
  Running: { id: "web.components.ui.phase_chip.running", defaultMessage: "Running", description: "Phase badge: label for a server that is running" },
  Stopping: { id: "web.components.ui.phase_chip.stopping", defaultMessage: "Stopping", description: "Phase badge: label for a server that is stopping" },
  Stopped: { id: "web.components.ui.phase_chip.stopped", defaultMessage: "Stopped", description: "Phase badge: label for a server that is stopped" },
  Suspending: { id: "web.components.ui.phase_chip.suspending", defaultMessage: "Suspending", description: "Phase badge: label for a server that is being suspended" },
  Suspended: { id: "web.components.ui.phase_chip.suspended", defaultMessage: "Suspended", description: "Phase badge: label for a server that is suspended" },
  Resuming: { id: "web.components.ui.phase_chip.resuming", defaultMessage: "Resuming", description: "Phase badge: label for a suspended server that is resuming" },
  Failed: { id: "web.components.ui.phase_chip.failed", defaultMessage: "Failed", description: "Phase badge: label for a resource whose last operation failed" },
  Succeeded: { id: "web.components.ui.phase_chip.succeeded", defaultMessage: "Succeeded", description: "Phase badge: label for a backup or restore that finished successfully" },
  Asleep: { id: "web.components.ui.phase_chip.asleep", defaultMessage: "Asleep", description: "Phase badge: label for a suspended server that is asleep (derived display state)" },
});

function phaseLabel(intl: IntlShape, value: string): string {
  return Object.prototype.hasOwnProperty.call(phaseMessages, value)
    ? intl.formatMessage(phaseMessages[value as keyof typeof phaseMessages])
    : value;
}

// PhaseChip renders a server/backup/restore phase badge.
export function PhaseChip({ phase, asleep, size = "sm", className }: {
  phase?: string;
  asleep?: boolean;
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  const intl = useOptionalIntl();
  const p = phase ?? "Pending";
  const label = phaseLabel(intl, asleep ? "Asleep" : p);
  const color = phaseColorMap[p] ?? "default";

  return (
    <HeroChip
      color={color}
      variant="soft"
      size={size}
      data-phase={p}
      data-color={color}
      data-asleep={asleep}
      data-size={size}
      className={className}
    >
      {label}
    </HeroChip>
  );
}
