import { useMemo, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { convertMem, formatMemQuantity, parseMemQuantity, type MemUnit } from "@/lib/quantity";

type StorageUnit = Extract<MemUnit, "Mi" | "Gi" | "Ti">;

const STORAGE_UNIT_OPTIONS: { value: StorageUnit; label: string }[] = [
  { value: "Mi", label: "MiB" },
  { value: "Gi", label: "GiB" },
  { value: "Ti", label: "TiB" },
];

export interface StorageInputProps {
  value: string; // Kubernetes quantity (e.g. "20Gi", "1536Mi"); "" when unset
  onChange: (qty: string) => void; // emits a quantity, or "" when the box is cleared
  label: string; // accessible name; the box is "<label> value", the unit select "<label> unit"
  id?: string;
  disabled?: boolean;
  invalid?: boolean;
}

// Display-only rounding so a unit switch doesn't show float noise.
function roundDisplay(n: number): number {
  if (Number.isInteger(n)) return n;
  return Math.round(n * 10000) / 10000;
}

/**
 * StorageInput: number box + unit dropdown (MiB/GiB/TiB) for a storage size.
 * The incoming quantity is never rewritten unless the user edits the number:
 * a value that does not fit a whole unit (1536Mi) loads and saves unchanged,
 * because the parsed amount keeps its own binary unit and a commit that equals
 * what is shown emits nothing.
 */
export function StorageInput({ value, onChange, label, id, disabled = false, invalid = false }: StorageInputProps) {
  const intl = useOptionalIntl();
  const parsed = useMemo(() => parseMemQuantity(value), [value]);
  const [unit, setUnit] = useState<StorageUnit>(parsed && parsed.unit !== "Ki" ? parsed.unit : parsed ? "Mi" : "Gi");
  // Raw text while typing; null = not editing (show the derived value).
  const [buffer, setBuffer] = useState<string | null>(null);
  // The number the user last typed. Picking a unit right after typing (the blur
  // already saved it in the old unit) applies that unit to the typed number, so
  // "500" then MiB means 500 MiB, not 500 GiB shown as 512000 MiB.
  const typed = useRef<number | null>(null);

  const derivedText = parsed ? String(roundDisplay(convertMem(parsed, unit).value)) : "";

  const handleBlur = () => {
    const text = buffer;
    setBuffer(null);
    if (text === null || text === derivedText) return; // nothing edited
    if (text.trim() === "") {
      typed.current = null;
      onChange("");
      return;
    }
    const num = Number(text);
    if (!Number.isFinite(num) || num <= 0) return; // keep the previous value
    typed.current = num;
    onChange(formatMemQuantity({ value: num, unit }));
  };

  const handleUnitChange = (next: string) => {
    const nextUnit = next as StorageUnit;
    // Text still in the box (no blur yet) counts as typed too.
    const pending = buffer !== null && buffer !== derivedText && Number(buffer) > 0 ? Number(buffer) : null;
    const num = pending ?? typed.current;
    setBuffer(null);
    setUnit(nextUnit);
    if (num !== null) {
      typed.current = num;
      onChange(formatMemQuantity({ value: num, unit: nextUnit }));
    }
  };

  return (
    <div className="flex items-center gap-2 sm:gap-3">
      <input
        id={id}
        type="number"
        min={0}
        step="any"
        value={buffer ?? derivedText}
        onChange={(e) => setBuffer(e.target.value)}
        onBlur={handleBlur}
        disabled={disabled}
        aria-invalid={invalid || undefined}
        aria-label={intl.formatMessage({ id: "web.components.ui.storage_input.value_label", defaultMessage: "{name} value", description: "Storage input: accessible name of the number field; {name} is the field name, e.g. Storage size" }, { name: label })}
        className={cn(
          "h-9 w-24 rounded-md border bg-surface px-3 text-sm text-fg",
          "placeholder:text-muted focus:outline-hidden focus:ring-1",
          invalid ? "border-danger focus:border-danger focus:ring-danger" : "border-border focus:border-primary focus:ring-primary",
          "disabled:opacity-50",
        )}
      />
      <select
        id={id ? `${id}-unit` : undefined}
        value={unit}
        disabled={disabled}
        onChange={(e) => handleUnitChange(e.target.value)}
        aria-label={intl.formatMessage({ id: "web.components.ui.storage_input.unit_label", defaultMessage: "{name} unit", description: "Storage input: accessible name of the unit select; {name} is the field name, e.g. Storage size" }, { name: label })}
        className="h-9 w-24 rounded-md border border-border bg-surface px-3 text-sm text-fg focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary disabled:opacity-50"
      >
        {STORAGE_UNIT_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
    </div>
  );
}
