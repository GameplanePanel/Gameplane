import { useState, useMemo } from "react";
import { defineMessages, type MessageDescriptor } from "react-intl";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import {
  formatCpuQuantity,
  convertCpu,
  cpuCores,
  parseCpuQuantity,
  formatMemQuantity,
  convertMem,
  memBytes,
  parseMemQuantity,
  type CpuAmount,
  type MemAmount,
  type CpuUnit,
  type MemUnit,
} from "@/lib/quantity";

export interface ResourceInputProps {
  kind: "cpu" | "memory";
  value: string; // canonical k8s quantity (e.g., "2", "500m", "4Gi", "512Mi")
  onChange: (qty: string) => void; // emits canonical k8s quantity string
  min?: number; // slider min in base units (cores for cpu, GiB for memory)
  max?: number; // slider max in base units
  step?: number; // slider step in base units
  disabled?: boolean;
  id?: string;
}

interface SelectOption {
  value: string;
  // Plain strings are unit symbols (mCPU, GiB) shown as-is; descriptors are
  // translatable words formatted at render.
  label: string | MessageDescriptor;
}

const unitWordMessages = defineMessages({
  cores: { id: "web.components.ui.resource_input.unit_cores", defaultMessage: "cores", description: "Resource input: CPU unit option in the unit select, meaning whole CPU cores (not millicores)" },
});

// Sliders step in the base unit (cores / GiB). Fine floors + steps so the
// slider can reach small, realistic sizes — 100m CPU (0.1 core) in 50m
// increments, 256Mi memory (0.25 GiB) in 256Mi increments — instead of
// snapping everything up to half a core / half a GiB.
const CPU_DEFAULTS = { min: 0.1, max: 16, step: 0.05 };
const MEM_DEFAULTS = { min: 0.25, max: 64, step: 0.25 };

const CPU_UNIT_OPTIONS: SelectOption[] = [
  { value: "cores", label: unitWordMessages.cores },
  { value: "m", label: "mCPU" },
];

const MEM_UNIT_OPTIONS: SelectOption[] = [
  { value: "Ki", label: "KiB" },
  { value: "Mi", label: "MiB" },
  { value: "Gi", label: "GiB" },
  { value: "Ti", label: "TiB" },
];

// Round a value for passive display so unit switches don't surface float
// noise (e.g. 100 MiB shown in GiB as "0.09765625"). Display-only — the
// emitted quantity always derives from the full-precision base magnitude.
function roundDisplay(n: number): number {
  if (Number.isInteger(n)) return n;
  return Math.round(n * 10000) / 10000;
}

/**
 * ResourceInput: a reusable control combining slider + numeric input + unit
 * dropdown for selecting CPU and memory resources. The slider axis is always
 * the base unit (cores for CPU, GiB for memory); the input shows the value in
 * the currently selected display unit; every interaction is converted back to
 * a canonical Kubernetes quantity string via `onChange`.
 *
 * Built from plain native elements (input[type=range], input[type=number],
 * select) rather than a HeroUI Slider/Select — HeroUI v3 has no Slider
 * component with this axis+unit-conversion shape, and this control's data
 * flow (parse → base magnitude → convert → emit) is unrelated to rendering,
 * so a shared hero/ primitive isn't warranted for it.
 */
export function ResourceInput({
  kind,
  value,
  onChange,
  min: userMin,
  max: userMax,
  step: userStep,
  disabled = false,
  id,
}: ResourceInputProps) {
  const defaults = kind === "cpu" ? CPU_DEFAULTS : MEM_DEFAULTS;
  const minBase = userMin ?? defaults.min;
  const maxBase = userMax ?? defaults.max;
  const stepBase = userStep ?? defaults.step;

  // Parse the canonical value into a base magnitude (cores / GiB) plus its
  // natural display amount. An unparseable/empty value is treated as the min.
  const parsed = useMemo(() => {
    if (kind === "cpu") {
      const amt = parseCpuQuantity(value);
      if (!amt) {
        return { base: minBase, amount: { value: minBase, unit: "cores" } as CpuAmount };
      }
      return { base: cpuCores(amt), amount: amt };
    }
    const amt = parseMemQuantity(value);
    if (!amt) {
      return { base: minBase, amount: { value: minBase, unit: "Gi" } as MemAmount };
    }
    return { base: memBytes(amt) / 1024 ** 3, amount: amt };
  }, [value, kind, minBase]);

  // Display unit — initialised from the parsed value, thereafter driven by the
  // dropdown.
  const [displayUnit, setDisplayUnit] = useState<CpuUnit | MemUnit>(parsed.amount.unit);

  // Local edit buffer: while the user is typing we hold their raw text and do
  // NOT emit, so the field can pass through transient/invalid states ("", "0.",
  // "1.5") without being clamped or reformatted mid-keystroke (which, since the
  // input is otherwise controlled off `value`, would fight the user's typing).
  // `null` means "not editing — show the derived value"; we commit on blur.
  const [buffer, setBuffer] = useState<string | null>(null);

  const baseValue = Math.max(minBase, Math.min(maxBase, parsed.base));

  // Passive (derived) input text in the current display unit.
  const derivedText = useMemo(() => {
    if (kind === "cpu") {
      return String(roundDisplay(convertCpu(parsed.amount as CpuAmount, displayUnit as CpuUnit).value));
    }
    return String(roundDisplay(convertMem(parsed.amount as MemAmount, displayUnit as MemUnit).value));
  }, [parsed.amount, displayUnit, kind]);

  // Emit a canonical quantity for a base magnitude (cores / GiB), clamped to
  // [min, max].
  const emitBase = (base: number) => {
    const clamped = Math.max(minBase, Math.min(maxBase, base));
    if (kind === "cpu") {
      onChange(formatCpuQuantity({ value: clamped, unit: "cores" }));
    } else {
      onChange(formatMemQuantity({ value: clamped, unit: "Gi" }));
    }
  };

  const handleSliderChange = (next: number) => {
    setBuffer(null);
    emitBase(next);
  };

  const handleInputBlur = () => {
    const text = buffer;
    setBuffer(null);
    if (text === null) return; // focused but never edited — nothing to commit
    const num = Number(text);
    if (text.trim() === "" || !Number.isFinite(num)) {
      emitBase(minBase); // empty / garbage snaps to the min
      return;
    }
    if (kind === "cpu") {
      emitBase(cpuCores({ value: num, unit: displayUnit as CpuUnit }));
    } else {
      emitBase(memBytes({ value: num, unit: displayUnit as MemUnit }) / 1024 ** 3);
    }
  };

  const handleUnitChange = (next: string) => {
    setBuffer(null);
    setDisplayUnit(next as CpuUnit | MemUnit);
  };

  const intl = useOptionalIntl();
  const ariaLabel =
    kind === "cpu"
      ? intl.formatMessage({ id: "web.components.ui.resource_input.cpu_cores", defaultMessage: "CPU cores", description: "Resource input: accessible name of the CPU slider and its fields" })
      : intl.formatMessage({ id: "web.components.ui.resource_input.memory_gib", defaultMessage: "Memory (GiB)", description: "Resource input: accessible name of the memory slider and its fields" });
  const unitOptions = kind === "cpu" ? CPU_UNIT_OPTIONS : MEM_UNIT_OPTIONS;

  return (
    <div className="flex items-center gap-2 sm:gap-3">
      <input
        id={id}
        type="range"
        role="slider"
        aria-label={ariaLabel}
        min={minBase}
        max={maxBase}
        step={stepBase}
        value={baseValue}
        disabled={disabled}
        onChange={(e) => handleSliderChange(Number(e.target.value))}
        className="h-2 flex-1 cursor-pointer appearance-none rounded-full bg-border accent-primary disabled:cursor-not-allowed disabled:opacity-50"
      />
      <input
        id={id ? `${id}-input` : undefined}
        type="number"
        value={buffer ?? derivedText}
        onChange={(e) => setBuffer(e.target.value)}
        onBlur={handleInputBlur}
        disabled={disabled}
        step={stepBase}
        aria-label={intl.formatMessage({ id: "web.components.ui.resource_input.value_label", defaultMessage: "{name} value", description: "Resource input: accessible name of the number field; {name} is the resource name, e.g. CPU cores" }, { name: ariaLabel })}
        className={cn(
          "h-9 w-16 rounded-md border border-border bg-surface px-3 text-sm text-fg",
          "placeholder:text-muted focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary",
          "disabled:opacity-50",
        )}
      />
      <select
        id={id ? `${id}-unit` : undefined}
        value={displayUnit}
        disabled={disabled}
        onChange={(e) => handleUnitChange(e.target.value)}
        aria-label={intl.formatMessage({ id: "web.components.ui.resource_input.unit_label", defaultMessage: "{name} unit", description: "Resource input: accessible name of the unit select; {name} is the resource name, e.g. CPU cores" }, { name: ariaLabel })}
        className="h-9 w-24 rounded-md border border-border bg-surface px-3 text-sm text-fg focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary disabled:opacity-50"
      >
        {unitOptions.map((o) => (
          <option key={o.value} value={o.value}>
            {typeof o.label === "string" ? o.label : intl.formatMessage(o.label)}
          </option>
        ))}
      </select>
    </div>
  );
}
