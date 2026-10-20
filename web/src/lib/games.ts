import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

import { englishIntl } from "@/lib/i18n/format";

// Category grouping for the module catalog + Create-Server template picker.
// A module may declare several categories (GameTemplate.spec.categories /
// CatalogEntry.categories) — Minecraft is reasonably Sandbox, Survival and
// Creative at once. When a module declares none we fall back to a
// best-effort heuristic on the game slug, so third-party modules that
// predate the field still group sensibly. The dashboard builds its filter
// chips from the categories actually present rather than a fixed list.
// Category values stay English for matching; categoryLabel() gives the display text.

export const OTHER_CATEGORY = "Other";

const CATEGORY_MESSAGES = defineMessages({
  survival: {
    id: "web.lib.games.category.survival",
    defaultMessage: "Survival",
    description: "Module category name shown as a filter chip and badge: survival games",
  },
  sandbox: {
    id: "web.lib.games.category.sandbox",
    defaultMessage: "Sandbox",
    description: "Module category name shown as a filter chip and badge: sandbox games",
  },
  shooter: {
    id: "web.lib.games.category.shooter",
    defaultMessage: "Shooter",
    description: "Module category name shown as a filter chip and badge: shooter games",
  },
  other: {
    id: "web.lib.games.category.other",
    defaultMessage: "Other",
    description: "Module category name shown as a filter chip and badge: modules that fit no other category",
  },
});

// Known category values, spelled exactly as the heuristic emits them, mapped to
// their message. Any other value (including a module's own spelling) is shown as
// the module wrote it, so English output is unchanged.
const CATEGORY_LABELS = new Map<string, MessageDescriptor>([
  ["Survival", CATEGORY_MESSAGES.survival],
  ["Sandbox", CATEGORY_MESSAGES.sandbox],
  ["Shooter", CATEGORY_MESSAGES.shooter],
  ["Other", CATEGORY_MESSAGES.other],
]);

// categoryLabel returns the display text for a category value. The value itself
// is unchanged, so matching keeps working; only the shown text is localized.
export function categoryLabel(category: string, intl: IntlShape = englishIntl): string {
  const descriptor = CATEGORY_LABELS.get(category);
  return descriptor ? intl.formatMessage(descriptor) : category;
}

// gameCategory is the heuristic fallback used when a module declares no
// explicit category. Unknown games map to "Other".
export function gameCategory(game: string): string {
  const g = game.toLowerCase();
  if (/valheim|palworld|ark|rust|conan|7.?days|dayz/.test(g)) return "Survival";
  if (/minecraft|terraria|factorio|satisfactory|stardew/.test(g)) return "Sandbox";
  if (/cs2|cs.?go|csgo|tf2|valorant|insurgency|squad|left4dead/.test(g)) return "Shooter";
  return OTHER_CATEGORY;
}

// resolveCategories returns a module's declared categories, or a
// single-element heuristic fallback when it declares none usable.
export function resolveCategories(explicit: string[] | undefined, game: string): string[] {
  const declared = (explicit ?? []).map((c) => c.trim()).filter((c) => c.length > 0);
  return declared.length > 0 ? declared : [gameCategory(game)];
}

// matchesCategory decides whether a module belongs under a given filter chip.
// Comparison is case-insensitive to agree with categoryFilters, which collapses
// "Survival" and "survival" into a single chip — a case-sensitive match here
// would hide every module that spelled its category differently from the first
// one seen.
export function matchesCategory(
  explicit: string[] | undefined,
  game: string,
  chip: string,
): boolean {
  if (chip === "all") return true;
  const want = chip.toLowerCase();
  return resolveCategories(explicit, game).some((c) => c.toLowerCase() === want);
}

// matchesAllCategories returns true when a module matches ALL of the selected
// category chips. An empty selection means "no filter" (show everything).
// Comparison is case-insensitive to agree with categoryFilters/matchesCategory.
export function matchesAllCategories(
  explicit: string[] | undefined,
  game: string,
  selected: Set<string>,
): boolean {
  if (selected.size === 0) return true;
  const wants = new Set([...selected].map((c) => c.toLowerCase()));
  const resolvedLower = resolveCategories(explicit, game).map((c) => c.toLowerCase());
  return [...wants].every((want) => resolvedLower.includes(want));
}

// categoryFilters builds the ordered chip list from the resolved categories
// of every item in the catalog — one string[] per module. "all" first, then
// the distinct named categories sorted alphabetically, then "Other" last
// (only if some module actually falls into it). Dedupe is case-insensitive
// so "Survival" and "survival" do not become two chips; the first spelling
// seen wins, matching the API's merge.
export function categoryFilters(categories: string[][]): string[] {
  const bySlug = new Map<string, string>();
  for (const list of categories) {
    for (const c of list) {
      const key = c.toLowerCase();
      if (!bySlug.has(key)) bySlug.set(key, c);
    }
  }
  const present = [...bySlug.values()];
  const otherLower = OTHER_CATEGORY.toLowerCase();
  const named = present
    .filter((c) => c.toLowerCase() !== otherLower)
    .sort((a, b) => a.localeCompare(b));
  const tail = present.some((c) => c.toLowerCase() === otherLower) ? [OTHER_CATEGORY] : [];
  return ["all", ...named, ...tail];
}
