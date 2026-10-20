import { clsx, type ClassValue } from "clsx";
import { defineMessages, type IntlShape } from "react-intl";
import { twMerge } from "tailwind-merge";

import { englishIntl } from "@/lib/i18n/format";

// Descriptors with placeholders are passed inline to formatMessage (typed values).
const MESSAGES = defineMessages({
  due: {
    id: "web.lib.utils.due",
    defaultMessage: "due",
    description: "Schedule timing: the next scheduled run time has already passed",
  },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Swallows a rejection from a fire-and-forget call (e.g. a router
// navigation) whose result we don't act on. Wrapped in Promise.resolve()
// so it tolerates a caller that returns something other than a thenable —
// notably a bare `vi.fn()` test mock, which returns `undefined` — instead
// of assuming the value always has a `.catch`.
export function ignoreRejection(maybePromise: unknown): void {
  void Promise.resolve(maybePromise).catch(() => {
    /* intentionally ignored */
  });
}

// Upper-cases the first character. The operator's provisioning messages
// are lowercase sentence fragments ("pulling the game image"); this makes
// them presentable as a status line without mangling the rest.
export function capitalize(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

export function formatBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  const v = n / Math.pow(1024, i);
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

// Numbers are passed as strings so English keeps plain digits (no grouping separators).
export function formatRelative(iso?: string, intl: IntlShape = englishIntl): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60)       return intl.formatMessage({
    id: "web.lib.utils.seconds_ago",
    defaultMessage: "{count}s ago",
    description: "Relative time: a moment ago in seconds, e.g. 45s ago; {count} is a number of seconds",
  }, { count: String(s) });
  if (s < 3600)     return intl.formatMessage({
    id: "web.lib.utils.minutes_ago",
    defaultMessage: "{count}m ago",
    description: "Relative time in minutes, e.g. 5m ago; {count} is a number of minutes",
  }, { count: String(Math.floor(s / 60)) });
  if (s < 86400)    return intl.formatMessage({
    id: "web.lib.utils.hours_ago",
    defaultMessage: "{count}h ago",
    description: "Relative time in hours, e.g. 2h ago; {count} is a number of hours",
  }, { count: String(Math.floor(s / 3600)) });
  return intl.formatMessage({
    id: "web.lib.utils.days_ago",
    defaultMessage: "{count}d ago",
    description: "Relative time in days, e.g. 3d ago; {count} is a number of days",
  }, { count: String(Math.floor(s / 86400)) });
}

// formatRelativeFuture renders a forward-looking interval ("in 45m") for a
// timestamp expected to be in the future (e.g. a schedule's next run). Past
// or now reads as "due".
export function formatRelativeFuture(iso?: string, intl: IntlShape = englishIntl): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const s = Math.floor((t - Date.now()) / 1000);
  if (s <= 0)       return intl.formatMessage(MESSAGES.due);
  if (s < 60)       return intl.formatMessage({
    id: "web.lib.utils.seconds_until",
    defaultMessage: "in {count}s",
    description: "Relative time until a future event in seconds; {count} is a number of seconds",
  }, { count: String(s) });
  if (s < 3600)     return intl.formatMessage({
    id: "web.lib.utils.minutes_until",
    defaultMessage: "in {count}m",
    description: "Relative time until a future event in minutes; {count} is a number of minutes",
  }, { count: String(Math.floor(s / 60)) });
  if (s < 86400)    return intl.formatMessage({
    id: "web.lib.utils.hours_until",
    defaultMessage: "in {count}h",
    description: "Relative time until a future event in hours; {count} is a number of hours",
  }, { count: String(Math.floor(s / 3600)) });
  return intl.formatMessage({
    id: "web.lib.utils.days_until",
    defaultMessage: "in {count}d",
    description: "Relative time until a future event in days; {count} is a number of days",
  }, { count: String(Math.floor(s / 86400)) });
}

// parseQuantityToBytes parses a size string into bytes. Accepts plain byte
// counts ("5368709120"), Kubernetes binary-SI ("5Gi", "512Mi") and decimal-SI
// ("5G", "500M") suffixes, and human "GiB"/"GB" forms. Returns 0 for empty or
// unparseable input, so it's safe to sum across a list.
export function parseQuantityToBytes(s?: string): number {
  if (!s) return 0;
  const m = /^\s*([0-9.]+)\s*([A-Za-z]*)\s*$/.exec(s);
  if (!m) return 0;
  const n = parseFloat(m[1]);
  if (Number.isNaN(n)) return 0;
  let unit = m[2].toUpperCase();
  if (unit.endsWith("B")) unit = unit.slice(0, -1); // GiB->GI, GB->G, B->""
  if (unit === "") return n;
  const factors: Record<string, number> = {
    KI: 1024, MI: 1024 ** 2, GI: 1024 ** 3, TI: 1024 ** 4, PI: 1024 ** 5,
    K: 1000, M: 1000 ** 2, G: 1000 ** 3, T: 1000 ** 4, P: 1000 ** 5,
  };
  return (factors[unit] ?? 0) * n;
}

// formatCores renders a CPU core quantity, trimming the floating-point
// noise that shows up when summing metrics-server's sub-core readings
// across nodes (e.g. 0.646 + 0.646 -> 1.2919999999999998).
export function formatCores(n: number): string {
  return Number.isInteger(n) ? `${n}` : n.toFixed(2);
}

export interface StorageReading {
  valueText: string;
  // subText is undefined when totalBytes is unknown (0/undefined) — callers
  // fall back to their own placeholder ("—").
  subText?: string;
  overcommitted: boolean;
}

// describeStorageProvisioned turns the cluster stats' {usedStorageBytes,
// totalStorageBytes} pair into copy that's honest about what they measure:
// usedStorageBytes is PersistentVolume capacity *provisioned* to workloads,
// not disk usage, and totalStorageBytes is node *physical* disk — with
// networked storage (Ceph, EBS, …) provisioned can legitimately exceed
// physical. Flags that case as `overcommitted` so callers can present it as
// an explicit state rather than a >100% bar.
export function describeStorageProvisioned(
  usedBytes?: number,
  totalBytes?: number,
  intl: IntlShape = englishIntl,
): StorageReading {
  const used = usedBytes ?? 0;
  const total = totalBytes ?? 0;
  const overcommitted = total > 0 && used > total;
  const valueText = formatBytes(used);
  if (!total) return { valueText, overcommitted: false };
  const subText = overcommitted
    ? intl.formatMessage({
      id: "web.lib.utils.storage_overcommitted",
      defaultMessage: "of {total} physical — overcommitted",
      description: "Storage capacity line when provisioned storage exceeds physical disk; {total} is a formatted size such as 40 GB",
    }, { total: formatBytes(total) })
    : intl.formatMessage({
      id: "web.lib.utils.storage_of_physical",
      defaultMessage: "of {total} physical",
      description: "Storage capacity line: provisioned storage out of node physical disk; {total} is a formatted size such as 40 GB",
    }, { total: formatBytes(total) });
  return { valueText, subText, overcommitted };
}

export function formatUptime(startedAt?: string, intl: IntlShape = englishIntl): string {
  if (!startedAt) return "—";
  const t = new Date(startedAt).getTime();
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return intl.formatMessage({
    id: "web.lib.utils.uptime_days_hours",
    defaultMessage: "{days}d {hours}h",
    description: "Server uptime in days and hours, e.g. 2d 5h; {days} and {hours} are numbers",
  }, { days: String(d), hours: String(h) });
  if (h) return intl.formatMessage({
    id: "web.lib.utils.uptime_hours_minutes",
    defaultMessage: "{hours}h {minutes}m",
    description: "Server uptime in hours and minutes, e.g. 3h 12m; {hours} and {minutes} are numbers",
  }, { hours: String(h), minutes: String(m) });
  return intl.formatMessage({
    id: "web.lib.utils.uptime_minutes",
    defaultMessage: "{minutes}m",
    description: "Server uptime in minutes, e.g. 12m; {minutes} is a number",
  }, { minutes: String(m) });
}
