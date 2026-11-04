import { parseQuantityToBytes } from "./utils";
import { isValidQuantity } from "./validation";

export type CpuUnit = "cores" | "m"; // "m" = millicpu (mCPU)
export type MemUnit = "Ki" | "Mi" | "Gi" | "Ti"; // binary, Kubernetes-native

export interface CpuAmount {
  value: number;
  unit: CpuUnit;
}

export interface MemAmount {
  value: number;
  unit: MemUnit;
}

/**
 * Parse a canonical Kubernetes CPU quantity into a display amount, picking a natural unit.
 * Examples:
 *   "2" -> {value: 2, unit: "cores"}
 *   "500m" -> {value: 500, unit: "m"}
 *   "1.5" -> {value: 1.5, unit: "cores"}
 *
 * Returns null for empty, undefined, or invalid input.
 */
export function parseCpuQuantity(qty: string | undefined): CpuAmount | null {
  if (!qty || !isValidQuantity(qty)) {
    return null;
  }

  if (qty.endsWith("m")) {
    const n = Number(qty.slice(0, -1));
    if (!Number.isFinite(n)) {
      return null;
    }
    return { value: n, unit: "m" };
  }

  const n = Number(qty);
  if (!Number.isFinite(n)) {
    return null;
  }
  return { value: n, unit: "cores" };
}

/**
 * Parse a canonical Kubernetes memory quantity into a display amount, picking a natural unit.
 * Accepts binary (Ki/Mi/Gi/Ti), decimal (K/M/G/T), and byte counts, normalizing to the
 * nearest whole binary unit.
 * Examples:
 *   "4Gi" -> {value: 4, unit: "Gi"}
 *   "512Mi" -> {value: 512, unit: "Mi"}
 *   "4G" -> {value: 3.814..., unit: "Gi"} (4000000000 bytes ≈ 3.73 Gi, but we pick the best unit)
 *
 * Returns null for empty, undefined, or invalid input.
 */
export function parseMemQuantity(qty: string | undefined): MemAmount | null {
  if (!qty || !isValidQuantity(qty)) {
    return null;
  }

  const bytes = parseQuantityToBytes(qty);
  if (bytes <= 0) {
    return null;
  }

  const magnitudes: Record<MemUnit, number> = {
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
  };
  const order: MemUnit[] = ["Ki", "Mi", "Gi", "Ti"];

  // Binary-suffixed input keeps its source unit, but promotes to a larger one
  // while the value stays a whole multiple: "512Mi" stays Mi, "4096Mi" → 4Gi,
  // and a fractional "0.5Gi" is preserved as Gi rather than demoted to 512Mi.
  const suffix = /(Ki|Mi|Gi|Ti)$/.exec(qty);
  if (suffix) {
    let unit = suffix[1] as MemUnit;
    let value = bytes / magnitudes[unit];
    let i = order.indexOf(unit);
    while (i < order.length - 1 && value >= 1024 && value % 1024 === 0) {
      value /= 1024;
      i += 1;
      unit = order[i];
    }
    return { value, unit };
  }

  // Bare byte counts and decimal-SI inputs have no clean binary source unit:
  // pick the largest unit that stays >= 1, falling back to Ki when < 1 Ki.
  for (const unit of ["Ti", "Gi", "Mi", "Ki"] as MemUnit[]) {
    const value = bytes / magnitudes[unit];
    if (value >= 1) {
      return { value, unit };
    }
  }
  return { value: bytes / magnitudes.Ki, unit: "Ki" };
}

/**
 * Format a CPU display amount back to a canonical Kubernetes quantity string.
 * Examples:
 *   {value: 2, unit: "cores"} -> "2"
 *   {value: 0.5, unit: "cores"} -> "500m"
 *   {value: 500, unit: "m"} -> "500m"
 */
export function formatCpuQuantity(a: CpuAmount): string {
  if (a.unit === "m") {
    // Millicores: emit as-is if integer, otherwise round to avoid float noise.
    const n = Number.isInteger(a.value) ? a.value : Math.round(a.value);
    return `${n}m`;
  }

  // cores: if integer, emit plain; otherwise convert to millicores.
  if (Number.isInteger(a.value)) {
    return String(a.value);
  }
  const millicores = Math.round(a.value * 1000);
  return `${millicores}m`;
}

/**
 * Format a memory display amount back to a canonical Kubernetes quantity string.
 * Whole values keep their unit; a fractional value steps down to the next smaller
 * binary unit so the emitted string stays integral and lossless (matching the
 * long-standing `memToQty` convention in Resources.tsx). Examples:
 *   {value: 4, unit: "Gi"} -> "4Gi"
 *   {value: 512, unit: "Mi"} -> "512Mi"
 *   {value: 1.5, unit: "Gi"} -> "1536Mi"
 *   {value: 0.5, unit: "Mi"} -> "512Ki"
 */
export function formatMemQuantity(a: MemAmount): string {
  if (Number.isInteger(a.value)) {
    return `${a.value}${a.unit}`;
  }
  // Fractional: convert to the next smaller unit, which turns a 0.5-step value
  // into a whole number (e.g. 1.5Gi -> 1536Mi). Recurse until integral.
  const smaller: Record<MemUnit, MemUnit | null> = { Ti: "Gi", Gi: "Mi", Mi: "Ki", Ki: null };
  const next = smaller[a.unit];
  if (next) {
    return formatMemQuantity(convertMem(a, next));
  }
  // Sub-Ki fractional (only reachable for tiny values): emit whole bytes.
  return String(Math.round(a.value * 1024));
}

/**
 * Convert a CPU display amount to another unit, preserving the physical amount.
 * Examples:
 *   {value: 2, unit: "cores"} -> "m" => {value: 2000, unit: "m"}
 *   {value: 500, unit: "m"} -> "cores" => {value: 0.5, unit: "cores"}
 */
export function convertCpu(a: CpuAmount, to: CpuUnit): CpuAmount {
  const cores = a.unit === "cores" ? a.value : a.value / 1000;
  return to === "cores" ? { value: cores, unit: "cores" } : { value: cores * 1000, unit: "m" };
}

/**
 * Convert a memory display amount to another unit, preserving the physical amount.
 * Examples:
 *   {value: 4, unit: "Gi"} -> "Mi" => {value: 4096, unit: "Mi"}
 *   {value: 4096, unit: "Mi"} -> "Gi" => {value: 4, unit: "Gi"}
 */
export function convertMem(a: MemAmount, to: MemUnit): MemAmount {
  const magnitudes: Record<MemUnit, number> = {
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
  };

  const bytes = a.value * magnitudes[a.unit];
  const value = bytes / magnitudes[to];
  return { value, unit: to };
}

/**
 * Get the canonical core count from a CPU display amount.
 * Examples:
 *   {value: 500, unit: "m"} -> 0.5
 *   {value: 2, unit: "cores"} -> 2
 */
export function cpuCores(a: CpuAmount): number {
  return a.unit === "cores" ? a.value : a.value / 1000;
}

/**
 * Get the canonical byte count from a memory display amount.
 * Examples:
 *   {value: 4, unit: "Gi"} -> 4294967296
 *   {value: 512, unit: "Mi"} -> 536870912
 */
export function memBytes(a: MemAmount): number {
  const magnitudes: Record<MemUnit, number> = {
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
  };

  return a.value * magnitudes[a.unit];
}

/**
 * Get the core count of a canonical CPU quantity, so amounts written in
 * different forms compare equal ("2" and "2000m" are both 2). Empty or
 * invalid input counts as 0.
 */
export function cpuQuantityCores(qty: string | undefined): number {
  const amount = parseCpuQuantity(qty);
  return amount ? cpuCores(amount) : 0;
}

/**
 * Get the byte count of a canonical memory quantity ("4Gi" and "4096Mi" are
 * equal). Empty, invalid or zero input counts as 0.
 */
export function memQuantityBytes(qty: string | undefined): number {
  const amount = parseMemQuantity(qty);
  return amount ? memBytes(amount) : 0;
}
