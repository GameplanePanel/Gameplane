import type { ReactNode } from "react";
import { Card } from "@heroui/react";
import { cn } from "@/lib/utils";

// StatCard renders a stat value with label, optional icon, and optional sub-text.
// Composes HeroUI Card with Gameplane brand tokens (accent, success, warning, danger, violet).
// Icon color is controlled by the accent prop. The "violet" accent renders the
// dedicated --chart-stat token (OD-9), not the shared --color-violet used by
// operator-role chips elsewhere in the app. The "info", "teal" and "slate" accents
// render the Servers card tokens (--stat-players, --stat-storage, --stat-nodes).
// The warning prop (FR-011) shows an amber badge plus amber sub-text and never
// changes the icon colour.

const accentClass = {
  primary: "text-accent",
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
  violet: "text-chart-stat",
  info: "text-stat-players",
  teal: "text-stat-storage",
  slate: "text-stat-nodes",
} as const;

export type StatCardAccent = keyof typeof accentClass;

export function StatCard({
  label,
  value,
  sub,
  icon,
  accent = "primary",
  warning,
  className,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  icon?: ReactNode;
  accent?: StatCardAccent;
  /** Badge text; when set the card shows an amber badge and amber sub-text. */
  warning?: ReactNode;
  className?: string;
}) {
  return (
    <Card
      className={cn(
        "border border-border bg-surface p-4",
        className,
      )}
    >
      <div className="flex items-center justify-between">
        <span className="text-xs font-semibold uppercase tracking-wider text-muted">
          {label}
        </span>
        {icon && (
          <span className={cn("h-5 w-5", accentClass[accent])}>
            {icon}
          </span>
        )}
      </div>
      <div className="pt-2 font-sans text-2xl font-bold text-foreground">
        {value}
      </div>
      {warning && (
        <div className="pt-2">
          <span
            data-slot="stat-card-warning"
            className="inline-flex items-center rounded-full bg-warning/15 px-2 py-0.5 text-xs font-medium text-warning"
          >
            {warning}
          </span>
        </div>
      )}
      {sub && (
        <div className={cn("pt-1 text-xs", warning ? "text-warning" : "text-muted")}>
          {sub}
        </div>
      )}
    </Card>
  );
}
