import { useEffect, useState, type ReactNode } from "react";
import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import {
  Button,
  Input,
  Chip,
  Select,
  SelectValue,
  SelectTrigger,
  SelectIndicator,
  SelectPopover,
  ListBox,
  ListBoxItem,
  Tabs,
  Tab,
} from "@heroui/react";

import type { RegistryProject } from "@/types";
import { useResourceTarget, useResourceClient, resourceKey } from "@/lib/resourceTarget";
import { errorTextIntl } from "@/lib/errors";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

const PAGE = 24;

export type RegistrySort = "downloads" | "updated" | "newest";

const SORT_LABELS = defineMessages({
  downloads: { id: "web.components.registry_browser.sort_downloads", defaultMessage: "Downloads", description: "Registry browser: sort option that orders results by download count" },
  updated: { id: "web.components.registry_browser.sort_updated", defaultMessage: "Recently updated", description: "Registry browser: sort option that orders results by last update" },
  newest: { id: "web.components.registry_browser.sort_newest", defaultMessage: "Newest", description: "Registry browser: sort option that orders results by release date" },
});

const SORTS: { value: RegistrySort; label: MessageDescriptor }[] = [
  { value: "downloads", label: SORT_LABELS.downloads },
  { value: "updated", label: SORT_LABELS.updated },
  { value: "newest", label: SORT_LABELS.newest },
];

// Display labels for the provider switch.
const PROVIDER_LABELS: Record<string, string> = {
  modrinth: "Modrinth",
  curseforge: "CurseForge",
  thunderstore: "Thunderstore",
  hangar: "Hangar",
  factorio: "Factorio Mod Portal",
  steam: "Steam Workshop",
  spigot: "SpigotMC",
  github: "GitHub Releases",
  umod: "uMod",
  nexus: "Nexus Mods",
};

// providerLabel is exported so other mod surfaces (e.g. the id-managed
// mods editor) can label a provider consistently without duplicating the
// display-name table.
export function providerLabel(p: string): string {
  return PROVIDER_LABELS[p] ?? p.charAt(0).toUpperCase() + p.slice(1);
}

// Providers whose backend registry.Search honors SearchQuery.Category as a
// genuine category facet (api/internal/registry/modrinth.go,
// api/internal/registry/hangar.go). Every other provider either ignores the
// param (curseforge, thunderstore, factorio, steam, spigot, github, nexus)
// or repurposes it for something else (umod's Category selects a game, not
// a mod category) — the category chips would silently do nothing there, so
// they render disabled instead of implying a filter that isn't applied.
const CATEGORY_FILTERABLE_PROVIDERS = new Set(["modrinth", "hangar"]);

// RegistryBrowser is the shared full browser used by the Mods install page
// and the Modpacks tab: a provider switch (when a game declares more than
// one registry), a debounced search, a sort control, optional category
// chips, a paged result grid (load-more), and a default "popular" listing
// when the search is empty. Each result is rendered by renderItem with the
// project's own provider, including retained results during a provider switch.
export function RegistryBrowser({
  name,
  ns,
  type,
  categories,
  renderItem,
  pillButtons,
}: {
  name: string;
  ns?: string;
  type?: "mod" | "modpack";
  categories?: { value: string; label: string }[];
  renderItem: (project: RegistryProject, provider: string) => ReactNode;
  pillButtons?: boolean;
}) {
  const intl = useOptionalIntl();
  const target = useResourceTarget({ name, namespace: ns });
  const client = useResourceClient(target);
  const [term, setTerm] = useState("");
  const [debounced, setDebounced] = useState("");
  const [sort, setSort] = useState<RegistrySort>("downloads");
  const [category, setCategory] = useState("");
  const [picked, setPicked] = useState<string | undefined>(undefined);

  useEffect(() => {
    const t = setTimeout(() => setDebounced(term.trim()), 300);
    return () => clearTimeout(t);
  }, [term]);

  // Which registries this game offers (and which are usable). For the
  // modpacks browser, only providers that declare modpacks.
  const providersQ = useQuery({
    queryKey: resourceKey(target, "registry-providers"),
    queryFn: ({ signal }) => client.withSignal(signal).Servers.registryProviders(name, ns),
  });
  const available = (providersQ.data ?? []).filter(
    (p) => p.available && (type !== "modpack" || p.modpacks),
  );
  // Fall back to the first available provider until one is picked (or if a
  // stale pick is no longer offered).
  const provider = picked && available.some((p) => p.provider === picked) ? picked : available[0]?.provider;

  const q = useInfiniteQuery({
    queryKey: resourceKey(target, "registry", type ?? "mod", provider, debounced, sort, category),
    initialPageParam: 0,
    enabled: !!provider,
    // Keep the previous results on screen while a new search/sort/category
    // refetches. Without this, every debounced keystroke flips the query to
    // a pending state that blanks the grid — which unmounts result cards and
    // collapses any the user had expanded mid-browse.
    placeholderData: keepPreviousData,
    queryFn: ({ pageParam, signal }) =>
      client.withSignal(signal).Servers.searchRegistry(
        name,
        {
          q: debounced,
          provider,
          type,
          // A search term ranks by relevance; an empty browse uses the chosen sort.
          sort: debounced ? undefined : sort,
          category,
          limit: PAGE,
          offset: pageParam,
        },
        ns,
      ),
    getNextPageParam: (last, pages) => (last.length === PAGE ? pages.length * PAGE : undefined),
  });

  const items = q.data?.pages.flat() ?? [];
  const showChips = !!categories && categories.length > 0;
  const chipsInteractive = !!provider && CATEGORY_FILTERABLE_PROVIDERS.has(provider);

  // No usable provider (none declared, or all need config like a CurseForge key).
  if (!providersQ.isLoading && available.length === 0) {
    return <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.registry_browser.in_app_browse_isn_t_available_for_this", defaultMessage: "In-app browse isn’t available for this server.", description: "Registry browser: message shown instead of the results when no usable registry provider exists for this server, so in-app browsing is unavailable" })}</p>;
  }

  return (
    <div className="flex min-h-0 flex-col gap-3">
      {available.length > 1 && (
        <Tabs
          selectedKey={provider}
          onSelectionChange={(key) => setPicked(key as string)}
          aria-label={type === "modpack" ? intl.formatMessage({ id: "web.components.registry_browser.modpack_registry_aria", defaultMessage: "Modpack registry", description: "Registry browser: aria-label for the modpack registry" }) : intl.formatMessage({ id: "web.components.registry_browser.mod_registry_aria", defaultMessage: "Mod registry", description: "Registry browser: aria-label for the mod registry" })}
        >
          <Tabs.List className="w-fit">
            {available.map((p) => (
              <Tab key={p.provider} id={p.provider} className="text-xs">
                {providerLabel(p.provider)}
              </Tab>
            ))}
          </Tabs.List>
        </Tabs>
      )}

      <div className="flex items-center gap-2">
        <Input
          autoFocus
          className="flex-1"
          placeholder={type === "modpack" ? intl.formatMessage({ id: "web.components.registry_browser.search_modpacks", defaultMessage: "Search modpacks…", description: "Registry browser: search field placeholder for modpacks" }) : intl.formatMessage({ id: "web.components.registry_browser.search_mods", defaultMessage: "Search mods…", description: "Registry browser: search field placeholder for mods" })}
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          spellCheck={false}
        />
        <Select
          selectedKey={sort}
          onSelectionChange={(key) => setSort(key as RegistrySort)}
          isDisabled={!!debounced}
          aria-label={debounced ? intl.formatMessage({ id: "web.components.registry_browser.sort_disabled_aria", defaultMessage: "Sort (disabled, sorted by relevance while searching)", description: "Registry browser: aria-label of the sort control while it is disabled during search" }) : intl.formatMessage({ id: "web.components.registry_browser.sort_aria", defaultMessage: "Sort", description: "Registry browser: aria-label of the sort control" })}
          className="w-[220px]"
        >
          <SelectTrigger>
            <SelectValue />
            <SelectIndicator />
          </SelectTrigger>
          <SelectPopover>
            <ListBox
              items={SORTS}
            >
              {(s) => (
                <ListBoxItem key={s.value} id={s.value} textValue={intl.formatMessage({ id: "web.components.registry_browser.sort_label", defaultMessage: "Sort: {label}", description: "Registry browser: sort option text, {label} is the sort option name" }, { label: intl.formatMessage(s.label) })}>{intl.formatMessage({ id: "web.components.registry_browser.sort_label", defaultMessage: "Sort: {label}", description: "Registry browser: sort option text, {label} is the sort option name" }, { label: intl.formatMessage(s.label) })}</ListBoxItem>
              )}
            </ListBox>
          </SelectPopover>
        </Select>
      </div>

      {showChips && (
        <div className="flex flex-wrap gap-2 modpack-categories">
          {[{ value: "", label: intl.formatMessage({ id: "web.components.registry_browser.all_categories", defaultMessage: "All", description: "Registry browser: category chip that clears the category filter" }) }, ...(categories ?? [])].map((c) => {
            const active = category === c.value;
            if (!chipsInteractive) {
              return (
                <Chip
                  key={c.value || "all"}
                  size="sm"
                  variant="soft"
                  aria-disabled="true"
                  className="cursor-not-allowed rounded-full opacity-50"
                  title={intl.formatMessage({ id: "web.components.registry_browser.category_unavailable", defaultMessage: "Category filtering isn't available for {provider}.", description: "Registry browser: tooltip when a provider has no category filter; {provider} is the provider label" }, { provider: providerLabel(provider ?? "") })}
                >
                  {c.label}
                </Chip>
              );
            }
            return (
              <Chip
                key={c.value || "all"}
                size="sm"
                color={active ? "accent" : "default"}
                variant={active ? "primary" : "soft"}
                className={
                  active
                    ? "rounded-full"
                    : "rounded-full border border-foreground/20 bg-transparent text-muted"
                }
                onClick={() => setCategory(c.value)}
                role="button"
                tabIndex={0}
                aria-pressed={active}
                data-active={active}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    setCategory(c.value);
                  }
                }}
              >
                {c.label}
              </Chip>
            );
          })}
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        {q.isLoading || providersQ.isLoading ? (
          <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.registry_browser.loading", defaultMessage: "Loading…", description: "Registry browser: loading text shown while registry results are being fetched" })}</p>
        ) : q.isError ? (
          <p className="text-xs text-danger">{errText(q.error, intl)}</p>
        ) : items.length === 0 ? (
          <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.registry_browser.no_results", defaultMessage: "No results.", description: "Registry browser: empty-state text shown when the registry search returns nothing" })}</p>
        ) : (
          <>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              {items.map((p) => (
                <div key={`${p.provider}:${p.id}`}>{renderItem(p, p.provider)}</div>
              ))}
            </div>
            <div className="flex justify-center pt-3">
              <Button
                variant="outline"
                size="sm"
                className={pillButtons ? "rounded-full" : undefined}
                onPress={() => void q.fetchNextPage()}
                isDisabled={!q.hasNextPage || q.isFetchingNextPage || q.isPlaceholderData}
              >
                {q.isFetchingNextPage ? intl.formatMessage({ id: "web.components.registry_browser.load_more_loading", defaultMessage: "Loading…", description: "Registry browser: load-more button label while the next page loads" }) : intl.formatMessage({ id: "web.components.registry_browser.load_more", defaultMessage: "Load more", description: "Registry browser: button label to load the next page of results" })}
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function errText(err: unknown, intl: IntlShape): string {
  return errorTextIntl(intl, err, intl.formatMessage({ id: "web.components.registry_browser.request_failed", defaultMessage: "request failed", description: "Registry browser: fallback error text when a registry request fails without a server message" }));
}

// compactNum formats large download counts (e.g. 1.2M).
export function compactNum(n: number): string {
  return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

// RegistryIcon shows a registry icon with a graceful fallback when it's
// missing or the third-party CDN image fails to load.
export function RegistryIcon({ url, fallback }: { url?: string; fallback: ReactNode }) {
  const [failed, setFailed] = useState(false);
  if (!url || failed) return <>{fallback}</>;
  return (
    <img
      src={url}
      alt=""
      loading="lazy"
      className="h-9 w-9 shrink-0 rounded object-cover"
      onError={() => setFailed(true)}
    />
  );
}
