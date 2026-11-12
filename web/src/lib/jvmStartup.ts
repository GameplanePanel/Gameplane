import type { IntlShape } from "react-intl";

import type { GameServer, GameTemplate } from "@/types";
import { activeVersion } from "./capabilities";
import { memQuantityBytes } from "./quantity";
import { CONFIG_REDACTED_MARKER, validateConfig, type ConfigError, type ConfigField } from "./validation";

const heapNames = ["INIT_MEMORY", "MAX_MEMORY"] as const;
type HeapName = (typeof heapNames)[number];

export function isJvmField(field: ConfigField): boolean {
  if (field.target === "file") return false;
  if (field.name === "USE_AIKAR_FLAGS") return field.type === "bool";
  return field.type === "string" && ["INIT_MEMORY", "MAX_MEMORY", "JVM_OPTS", "JVM_XX_OPTS"].includes(field.name);
}

interface Heap {
  flag?: string;
  bytes?: number;
  invalid?: boolean;
}

function parseHeap(name: HeapName, value: string, limit: number): Heap {
  const initial = name === "INIT_MEMORY";
  const percentage = /^(\d+(?:\.\d+)?)\s*%$/.exec(value);
  if (percentage && Number(percentage[1]) <= 100) {
    return {
      flag: `-XX:${initial ? "Initial" : "Max"}RAMPercentage=${percentage[1]}`,
      bytes: limit ? limit * Number(percentage[1]) / 100 : undefined,
    };
  }
  const fixed = /^(\d+)([kmgt]?)$/i.exec(value);
  if (!fixed || Number(fixed[1]) === 0) return { invalid: true };
  return {
    flag: `-Xm${initial ? "s" : "x"}${value}`,
    bytes: Number(fixed[1]) * 1024 ** ["", "k", "m", "g", "t"].indexOf(fixed[2].toLowerCase()),
  };
}

export function jvmStartup(template: GameTemplate | undefined, server: GameServer, intl: IntlShape) {
  const fields = template?.spec.game === "minecraft-java" ? (template.spec.configSchema ?? []).filter(isJvmField) : [];
  const version = activeVersion(template, server);
  const image = server.spec.image || version?.image || template?.spec.image || "";
  const itzg = /^(?:(?:docker\.io|index\.docker\.io)\/)?itzg\/minecraft-server(?=[:@]|$)/.test(image);
  const limit = memQuantityBytes(server.spec.resources?.limits?.memory ?? template?.spec.resources?.limits?.memory);
  const flags: string[] = [];
  const errors: ConfigError[] = [];
  const heaps: Partial<Record<HeapName, Heap>> = {};
  if (!itzg || template?.spec.command?.length || !fields.some((f) => heapNames.some((name) => f.name === name))) {
    return { fields, flags, errors };
  }

  // Resolve the operator's environment layers before applying the launcher's
  // MEMORY fallback. null means a valueFrom or expansion is unknown here.
  const base = [...(template?.spec.env ?? []), ...(version?.env ?? [])];
  const resolveEnv = (name: string, fallback: string | null): string | null => {
    const supplied = [...(server.spec.env ?? [])].reverse().find((e) => e.name === name);
    const field = template?.spec.configSchema?.find((f) => f.target !== "file" && f.name === name);
    let configured = field ? server.spec.config?.[name] ?? field.default ?? "" : "";
    if (!configured && field?.autoFromMemoryLimit) {
      const mib = Math.floor(limit * field.autoFromMemoryLimit.percent / 100 / 1024 ** 2);
      if (mib > 0) configured = `${mib}M`;
    }
    const inherited = [...base].reverse().find((e) => e.name === name);
    const env = supplied ?? (configured ? { value: configured } : inherited);
    if (!env) return fallback;
    if ("valueFrom" in env && env.valueFrom) return null;
    const value = env.value ?? "";
    return value === CONFIG_REDACTED_MARKER || value.includes("$(") ? null : value;
  };
  const memory = resolveEnv("MEMORY", "1G");
  const values = { INIT_MEMORY: resolveEnv("INIT_MEMORY", memory), MAX_MEMORY: resolveEnv("MAX_MEMORY", memory) };
  for (const name of heapNames) {
    const other = name === "INIT_MEMORY" ? values.MAX_MEMORY : values.INIT_MEMORY;
    const value = values[name] === "" && other ? memory : values[name];
    if (!value) continue;
    const heap = parseHeap(name, value, limit);
    heaps[name] = heap;
    if (heap.flag) flags.push(heap.flag);
    if (heap.invalid) {
      const text = intl.formatMessage({ id: "web.jvm.heap_format", defaultMessage: "Enter a JVM heap size such as 512M or 2G, or a percentage from 0% to 100%.", description: "Settings: invalid Minecraft Java heap value" });
      errors.push({ name, text, message: text });
    }
  }
  const initial = heaps.INIT_MEMORY?.bytes;
  const maximum = heaps.MAX_MEMORY?.bytes;
  if (initial !== undefined && maximum !== undefined && initial > maximum) {
    const text = intl.formatMessage({ id: "web.jvm.heap_order", defaultMessage: "The initial heap exceeds the maximum heap. Lower the initial heap or raise the maximum.", description: "Settings: the independently resolved initial Java heap is larger than the maximum heap" });
    errors.push({ name: fields.some((f) => f.name === "INIT_MEMORY") ? "INIT_MEMORY" : "MAX_MEMORY", text, message: text });
  }
  return { fields, flags, errors };
}

export function settingsConfigErrors(template: GameTemplate, server: GameServer, intl: IntlShape): ConfigError[] {
  return [
    ...validateConfig(template.spec.configSchema ?? [], server.spec.config ?? {}, intl),
    ...jvmStartup(template, server, intl).errors,
  ];
}
