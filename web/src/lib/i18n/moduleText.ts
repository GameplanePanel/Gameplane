import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { createIntl } from "react-intl";

import { Modules } from "@/lib/endpoints";

import { useOptionalIntl } from "./format";

// Module translations (spec 019 T089, FR-032, contracts/module-locales.md).
// A module may ship locales/<tag>.json; GET /modules/{name}/locales/{tag}
// returns its valid entries as a flat id -> ICU message map. The template's
// own text is the English default, so English never fetches anything.

export type ModuleText = (id: string, fallback: string) => string;

// identityText shows the template's own text.
export const identityText: ModuleText = (_id, fallback) => fallback;

// moduleIdSegment escapes a YAML name as one id segment, like i18n/modlocale
// Escape: "~" becomes "~0", then "." becomes "~1".
export function moduleIdSegment(name: string): string {
  return name.replaceAll("~", "~0").replaceAll(".", "~1");
}

export const moduleIds = {
  templateDisplayName: "template.displayName",
  templateDescription: "template.description",
  versionDisplayName: (id: string): string => `versions.${moduleIdSegment(id)}.displayName`,
  configDisplayName: (name: string): string => `config.${moduleIdSegment(name)}.displayName`,
  configDescription: (name: string): string => `config.${moduleIdSegment(name)}.description`,
  configOption: (name: string, value: string): string =>
    `config.${moduleIdSegment(name)}.options.${moduleIdSegment(value)}`,
};

function fetchesTranslations(locale: string): boolean {
  const tag = locale.toLowerCase();
  return tag !== "en" && !tag.startsWith("en-");
}

// moduleTextFrom formats a module's translations for locale; ids it lacks
// show the fallback text.
export function moduleTextFrom(locale: string, messages: Record<string, string> | undefined): ModuleText {
  if (!messages || Object.keys(messages).length === 0) return identityText;
  const intl = createIntl({ locale, defaultLocale: "en", messages, onError: () => {} });
  return (id, fallback) => {
    if (!Object.hasOwn(messages, id)) return fallback;
    const text: unknown = intl.formatMessage({ id, description: "Module text; the note comes from the module's locales/en.json" });
    return typeof text === "string" && text !== "" ? text : fallback;
  };
}

const STALE_MS = 5 * 60_000;

function localesQuery(name: string, tag: string, cluster: string) {
  return {
    queryKey: ["module-locales", cluster, name, tag],
    queryFn: () => Modules.locales(name, tag, cluster),
    staleTime: STALE_MS,
    retry: false,
  };
}

// useModuleText returns the text accessor for one module in the active language.
export function useModuleText(moduleName: string | undefined, cluster = "local"): ModuleText {
  const { locale } = useOptionalIntl();
  const enabled = !!moduleName && fetchesTranslations(locale);
  const { data } = useQuery({ ...localesQuery(moduleName ?? "", locale, cluster), enabled });
  return useMemo(() => (enabled ? moduleTextFrom(locale, data) : identityText), [enabled, locale, data]);
}

// useModuleTexts is useModuleText for a list of modules (the template picker).
export function useModuleTexts(moduleNames: readonly string[], cluster = "local"): (moduleName: string) => ModuleText {
  const { locale } = useOptionalIntl();
  const enabled = fetchesTranslations(locale);
  const names = [...new Set(moduleNames)].sort();
  const results = useQueries({ queries: names.map((name) => ({ ...localesQuery(name, locale, cluster), enabled })) });
  const texts = new Map<string, ModuleText>(
    names.map((name, i) => [name, enabled ? moduleTextFrom(locale, results[i]?.data) : identityText]),
  );
  return (moduleName) => texts.get(moduleName) ?? identityText;
}
