// Theme catalog (spec 043, T029): browse the themes from every enabled source,
// filter them, preview them and install, update or remove them for the signed-in
// user. Filtering is client-side over one catalog fetch, so the source and tag
// options stay complete while a filter is active.
import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Button, Input, ListBox, ListBoxItem, Select, ToggleButton } from "@heroui/react";
import { Search } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { SettingsNav } from "@/components/ui/SettingsNav";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { ThemeCard } from "@/components/themes/ThemeCard";
import {
  findInstalled,
  themeErrorText,
  THEME_CATALOG_KEY,
  useThemeActions,
} from "@/components/themes/useThemeActions";
import { installedThemesMessages, themeCatalogMessages, themePreviewBannerMessages } from "@/components/themes/messages";
import { Themes } from "@/lib/endpoints";
import { can, useMe } from "@/lib/auth";
import { THEMES_MANAGE_PERM } from "@/lib/themePermissions";
import { useThemePreview } from "@/lib/useThemePreview";
import { useOptionalIntl } from "@/lib/i18n/format";
import { formatWith } from "@/components/themes/formatWith";
import { cn } from "@/lib/utils";
import type { ThemeCatalogItem } from "@/types";

// Sentinel for "no filter" in the source and tag controls.
const ALL = "__all__";

const GRID_CLASS = "grid items-start gap-x-4 gap-y-6 md:grid-cols-2 xl:grid-cols-3";

export function ThemeCatalogPage() {
  const intl = useOptionalIntl();
  const navigate = useNavigate();
  const { data: me } = useMe();
  const canManage = can(me, THEMES_MANAGE_PERM);

  const [pageError, setPageError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [source, setSource] = useState(ALL);
  const [tag, setTag] = useState(ALL);
  const [removeTarget, setRemoveTarget] = useState<ThemeCatalogItem | null>(null);

  const catalog = useQuery({
    queryKey: [THEME_CATALOG_KEY],
    queryFn: () => Themes.catalog(),
  });
  const actions = useThemeActions(setPageError);
  const { start: startPreview } = useThemePreview();

  const themes = useMemo(() => catalog.data?.themes ?? [], [catalog.data]);
  const failingSources = catalog.data?.sources ?? [];

  const sourceNames = useMemo(() => [...new Set(themes.map((t) => t.source))].sort(), [themes]);

  // Tag options: keyed by the lower-cased tag, labelled with the first spelling seen.
  const tagOptions = useMemo(() => {
    const seen = new Map<string, string>();
    for (const t of themes) {
      for (const label of t.tags) {
        const key = label.toLowerCase();
        if (!seen.has(key)) seen.set(key, label);
      }
    }
    return [...seen.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [themes]);

  const needle = query.trim().toLowerCase();
  const filtersActive = needle !== "" || source !== ALL || tag !== ALL;
  const filtered = themes.filter((t) => {
    if (needle && !t.name.toLowerCase().includes(needle) && !t.displayName.toLowerCase().includes(needle)) {
      return false;
    }
    if (source !== ALL && t.source !== source) return false;
    if (tag !== ALL && !t.tags.some((label) => label.toLowerCase() === tag)) return false;
    return true;
  });

  const clearFilters = () => {
    setQuery("");
    setSource(ALL);
    setTag(ALL);
  };

  const handlePreview = async (item: ThemeCatalogItem) => {
    setPageError(null);
    try {
      const detail = await Themes.catalogEntry(item.id);
      // Invalid entries carry no tokens and cannot be previewed.
      if (!detail.tokens) return;
      const installedCopy = findInstalled(actions.installed, detail);
      const started = startPreview({
        catalogId: detail.id,
        installedId: installedCopy?.id ?? null,
        updateOnKeep: installedCopy !== undefined && installedCopy.version !== detail.version,
        displayName: detail.displayName,
        version: detail.version,
        tokens: detail.tokens,
        css: detail.css ?? null,
      });
      if (!started) {
        setPageError(intl.formatMessage(themePreviewBannerMessages.previewUnavailableSafeMode));
      }
    } catch (err) {
      setPageError(themeErrorText(err));
    }
  };

  const handleUpdate = (item: ThemeCatalogItem) => {
    const installed = findInstalled(actions.installed, item);
    if (installed) actions.update.mutate(installed.id);
  };

  const confirmRemove = () => {
    if (!removeTarget) return;
    const installed = findInstalled(actions.installed, removeTarget);
    if (!installed) {
      setRemoveTarget(null);
      return;
    }
    actions.remove.mutate(installed.id, { onSuccess: () => setRemoveTarget(null) });
  };

  let content: ReactNode;
  if (catalog.isLoading || actions.installedLoading) {
    content = (
      <div className={GRID_CLASS} aria-busy="true" data-testid="theme-catalog-skeleton">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="h-[420px] animate-pulse rounded-lg border border-border bg-card" />
        ))}
      </div>
    );
  } else if (catalog.isError || actions.installedError) {
    content = (
      <ErrorCard
        message={themeErrorText(catalog.isError ? catalog.error : actions.installedError)}
        onRetry={() => {
          if (catalog.isError) void catalog.refetch();
          if (actions.installedError) actions.retryInstalled();
        }}
      />
    );
  } else if (themes.length === 0 && !filtersActive) {
    content = (
      <div className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-center">
        <p className="text-sm font-medium text-foreground">{intl.formatMessage(themeCatalogMessages.emptyTitle)}</p>
        <p className="mt-1 text-sm text-muted">{intl.formatMessage(themeCatalogMessages.emptyText)}</p>
        {canManage ? (
          <div className="mt-4">
            <Button variant="outline" size="sm" onPress={() => void navigate({ to: "/settings/theme-sources" })}>
              {intl.formatMessage(themeCatalogMessages.goToSources)}
            </Button>
          </div>
        ) : null}
      </div>
    );
  } else if (filtered.length === 0) {
    content = (
      <div className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-center">
        <p className="text-sm font-medium text-foreground">{intl.formatMessage(themeCatalogMessages.noResultsTitle)}</p>
        <p className="mt-1 text-sm text-muted">{intl.formatMessage(themeCatalogMessages.noResultsText)}</p>
        <div className="mt-4">
          <Button variant="outline" size="sm" onPress={clearFilters}>
            {intl.formatMessage(themeCatalogMessages.clearFilters)}
          </Button>
        </div>
      </div>
    );
  } else {
    content = (
      <div className={GRID_CLASS} data-testid="theme-catalog-grid">
        {filtered.map((item) => (
          <ThemeCard
            key={item.id}
            item={item}
            busy={actions.busy}
            onPreview={(t) => void handlePreview(t)}
            onInstall={(t) => actions.install.mutate(t.id)}
            onUpdate={handleUpdate}
            onRemove={setRemoveTarget}
          />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage(themeCatalogMessages.title)}
        description={intl.formatMessage(themeCatalogMessages.description)}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        <SettingsNav
          active="theme"
          showAdminSections={can(me, "config:manage")}
          showThemeSources={canManage}
          onSelect={(key) => void navigate({ to: "/admin", search: { section: key } })}
        />

        <div className="min-w-0 space-y-6">
          {pageError ? (
            <div role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
              {pageError}
            </div>
          ) : null}

          {failingSources.map((s) => (
            <div
              key={s.name}
              role="status"
              className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
            >
              {formatWith(intl, themeCatalogMessages.sourceUnreachable, { name: s.name, error: s.lastError })}
            </div>
          ))}

          <div className="space-y-3">
            <div className="flex flex-col gap-3 md:flex-row md:items-center">
              <div className="relative flex-1">
                <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted" />
                <Input
                  className="w-full pl-9"
                  type="search"
                  placeholder={intl.formatMessage(themeCatalogMessages.search)}
                  aria-label={intl.formatMessage(themeCatalogMessages.search)}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
              </div>
              <Select
                className="md:w-56"
                aria-label={intl.formatMessage(themeCatalogMessages.allSources)}
                value={source}
                onChange={(v) => setSource(String(v))}
              >
                <Select.Trigger className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                  <Select.Value />
                  <Select.Indicator className="ml-auto h-4 w-4" />
                </Select.Trigger>
                <Select.Popover className="rounded border border-border">
                  <ListBox aria-label={intl.formatMessage(themeCatalogMessages.allSources)}>
                    <ListBoxItem id={ALL}>{intl.formatMessage(themeCatalogMessages.allSources)}</ListBoxItem>
                    {sourceNames.map((name) => (
                      <ListBoxItem key={name} id={name}>
                        {name}
                      </ListBoxItem>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>

            {tagOptions.length > 0 ? (
              <div className="flex flex-wrap gap-1 rounded-lg border border-border p-1">
                <ToggleButton
                  variant="ghost"
                  size="sm"
                  isSelected={tag === ALL}
                  onChange={() => setTag(ALL)}
                  className={cn(
                    "rounded px-3 py-1.5 text-sm text-muted hover:text-foreground",
                    tag === ALL && "bg-primary/15 text-primary",
                  )}
                >
                  {intl.formatMessage(themeCatalogMessages.allTags)}
                </ToggleButton>
                {tagOptions.map(([key, label]) => (
                  <ToggleButton
                    key={key}
                    variant="ghost"
                    size="sm"
                    isSelected={tag === key}
                    onChange={() => setTag(key)}
                    className={cn(
                      "rounded px-3 py-1.5 text-sm text-muted hover:text-foreground",
                      tag === key && "bg-primary/15 text-primary",
                    )}
                  >
                    {label}
                  </ToggleButton>
                ))}
              </div>
            ) : null}
          </div>

          {content}
        </div>
      </div>

      <ConfirmDialog
        open={removeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRemoveTarget(null);
        }}
        title={
          removeTarget
            ? formatWith(intl, installedThemesMessages.removeTitle, { name: removeTarget.displayName })
            : ""
        }
        description={intl.formatMessage(installedThemesMessages.removeText)}
        confirmLabel={intl.formatMessage(installedThemesMessages.removeConfirm)}
        destructive
        busy={actions.busy}
        onConfirm={confirmRemove}
      />
    </div>
  );
}
