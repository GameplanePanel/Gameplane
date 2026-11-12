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
  if (!itzg) return { fields, flags, errors };
  for (const name of heapNames) {
    const field = fields.find((f) => f.name === name);
    if (!field) continue;
    const override = [...(server.spec.env ?? [])].reverse().find((v) => v.name === name);
    const inherited = [...(template?.spec.env ?? []), ...(version?.env ?? [])].reverse().find((v) => v.name === name);
    let value = server.spec.config?.[name] ?? field.default ?? "";
    if (!value && field.autoFromMemoryLimit) {
      const mib = Math.floor(limit * field.autoFromMemoryLimit.percent / 100 / 1024 ** 2);
      if (mib > 0) value = `${mib}M`;
    }
    if (!value) value = inherited?.value ?? "";
    if (override) value = override.value ?? "";
    if (!value || value === CONFIG_REDACTED_MARKER || value.includes("$(")) continue;
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
    errors.push({ name: "INIT_MEMORY", text, message: text });
  }
  return { fields, flags, errors };
}

export function settingsConfigErrors(template: GameTemplate, server: GameServer, intl: IntlShape): ConfigError[] {
  return [
    ...validateConfig(template.spec.configSchema ?? [], server.spec.config ?? {}, intl),
    ...jvmStartup(template, server, intl).errors,
  ];
}
