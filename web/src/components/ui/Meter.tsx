// Meter renders a labelled horizontal usage bar (CPU, memory, storage, …).
// HeroUI-based version using semantic color tokens (bg-accent, bg-success, etc.)
// instead of legacy aliases. Pure SVG component with no HeroUI counterpart.

import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

const accentClass = {
  primary: "bg-accent",
  violet: "bg-violet",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
} as const;

export type MeterAccent = keyof typeof accentClass;

export function Meter({
  label,
  pct,
  sub,
  accent = "primary",
  unknown = false,
}: {
  label: string;
  pct: number;
  sub?: string;
  accent?: MeterAccent;
  // unknown renders "—" and an empty track instead of a percentage, for
  // when there's no measurement to show (e.g. no metrics-server) — a
  // meter reading 0% otherwise reads as "measured idle", not "unmeasured".
  unknown?: boolean;
}) {
  const intl = useOptionalIntl();
  return (
    <div>
      <div className="flex items-center justify-between text-[11px]">
        <span className="text-muted">{label}</span>
        <span className={cn("font-mono", unknown ? "text-muted" : "text-foreground")}>
          {unknown
            ? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })
            : intl.formatMessage({ id: "web.components.ui.meter.percent", defaultMessage: "{value}%", description: "Meter: usage percentage shown beside the bar; {value} is the whole-number percentage" }, { value: String(Math.max(0, Math.round(pct))) })}
        </span>
      </div>
      <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-surface">
        {!unknown && (
          <div
            className={`h-full ${accentClass[accent]}`}
            style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
          />
        )}
      </div>
      {sub && <div className="pt-1 text-[10px] text-muted">{sub}</div>}
    </div>
  );
}
