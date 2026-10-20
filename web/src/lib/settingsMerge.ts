import { defineMessages, type IntlShape } from "react-intl";

import { englishIntl } from "@/lib/i18n/format";
import type { GameServer } from "@/types";

const MESSAGES = defineMessages({
  conflict: {
    id: "web.lib.settings_merge.conflict",
    defaultMessage: "Server changed since you opened this page.",
    description: "Error shown when saving server settings after the server was changed elsewhere",
  },
});

export class SettingsConflictError extends Error {
  constructor(intl: IntlShape = englishIntl) {
    super(intl.formatMessage(MESSAGES.conflict));
    this.name = "SettingsConflictError";
  }
}

// Apply only edits made since the form was opened. Independent object keys can
// merge; arrays are a single field because their entries have no stable identity.
export function mergeDraftOntoLatest(
  draft: GameServer,
  baseline: GameServer,
  latest: GameServer,
  intl: IntlShape = englishIntl,
): GameServer {
  // Never carry edits from a deleted server onto a namesake replacement.
  if (baseline.metadata.uid !== latest.metadata.uid) throw new SettingsConflictError(intl);
  const out = structuredClone(latest);
  // These maps use absence and an empty object interchangeably. Clearing the
  // last baseline key must still preserve keys another writer added meanwhile.
  out.spec = mergeValue(
    { ...baseline.spec, config: baseline.spec.config ?? {} },
    { ...draft.spec, config: draft.spec.config ?? {} },
    { ...latest.spec, config: latest.spec.config ?? {} },
  ) as GameServer["spec"];
  if (Object.keys(out.spec.config ?? {}).length === 0) delete out.spec.config;
  out.metadata.labels = mergeMap(baseline.metadata.labels, draft.metadata.labels, latest.metadata.labels);
  out.metadata.annotations = mergeMap(baseline.metadata.annotations, draft.metadata.annotations, latest.metadata.annotations);
  return out;
}

function mergeMap(
  baseline: Record<string, string> | undefined,
  draft: Record<string, string> | undefined,
  latest: Record<string, string> | undefined,
): Record<string, string> | undefined {
  const out = mergeValue(baseline ?? {}, draft ?? {}, latest ?? {}) as Record<string, string>;
  return Object.keys(out).length ? out : undefined;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function ownValue(object: Record<string, unknown>, key: string): unknown {
  return Object.hasOwn(object, key) ? object[key] : undefined;
}

function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((value, i) => equal(value, b[i]));
  }
  if (isObject(a) && isObject(b)) {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    return [...keys].every((key) => equal(ownValue(a, key), ownValue(b, key)));
  }
  return false;
}

function mergeValue(baseline: unknown, draft: unknown, latest: unknown): unknown {
  if (equal(baseline, draft)) return structuredClone(latest);
  if (
    isObject(draft) && (baseline === undefined || isObject(baseline)) &&
    (latest === undefined || isObject(latest))
  ) {
    const before = baseline ?? {};
    const current = latest ?? {};
    const out = structuredClone(current);
    for (const key of new Set([...Object.keys(before), ...Object.keys(draft)])) {
      const value = mergeValue(ownValue(before, key), ownValue(draft, key), ownValue(current, key));
      if (value === undefined) delete out[key];
      else Object.defineProperty(out, key, { value, writable: true, enumerable: true, configurable: true });
    }
    return out;
  }
  if (equal(latest, baseline) || equal(latest, draft)) return structuredClone(draft);
  throw new SettingsConflictError();
}
