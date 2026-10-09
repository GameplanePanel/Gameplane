import { ModuleTarget } from "@/components/ModuleTarget";
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, Settings2, Upload } from "lucide-react";
import { Button, Input } from "@heroui/react";

import { ModuleCard } from "@/components/modules/ModuleCard";
import { InstallDialog } from "@/components/modules/InstallDialog";
import { UploadModuleDialog } from "@/components/modules/UploadModuleDialog";
import { BuildModuleDialog } from "@/components/modules/BuildModuleDialog";
import { ClusterModuleSources } from "@/components/modules/ModuleSourcesPanel";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { PageHeader } from "@/components/PageHeader";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { Modules, ModuleSources } from "@/lib/endpoints";
import { APIError } from "@/lib/api";
import { verifyForEntry } from "@/lib/verify";
import { resolveCategories, categoryFilters, matchesAllCategories } from "@/lib/games";
import type { CatalogEntry } from "@/types";
import { cn } from "@/lib/utils";

// ModulesPage renders the catalog merged across every ModuleSource. The
// install/upgrade/uninstall actions all hit /modules — install creates
// a Module CR, the operator's Module reconciler pulls the bundle and
// materializes the GameTemplate.
export function ModulesPage() {
  return <ModuleTarget>{(cluster, canManage) => <ModulesCatalog key={cluster} cluster={cluster} canManage={canManage} />}</ModuleTarget>;
}

function ModulesCatalog({ cluster, canManage }: { cluster: string; canManage: boolean }) {
  const qc = useQueryClient();
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["modules-catalog", cluster],
    queryFn: () => Modules.catalog(cluster),
    refetchInterval: 5_000, // pick up phase transitions promptly
  });
  const { data: sourcesData, isError: sourcesError, refetch: refetchSources } = useQuery({
    queryKey: ["module-sources", cluster],
    queryFn: () => ModuleSources.list(cluster),
  });

  const [q, setQ] = useState("");
  const [sourceFilter, setSourceFilter] = useState<string>("all");
  const [catFilter, setCatFilter] = useState<Set<string>>(new Set());
  const [installTarget, setInstallTarget] = useState<CatalogEntry | null>(null);
  const [uninstallTarget, setUninstallTarget] = useState<CatalogEntry | null>(null);
  const [removeUploadTarget, setRemoveUploadTarget] = useState<CatalogEntry | null>(null);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [buildOpen, setBuildOpen] = useState(false);
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const [pageError, setPageError] = useState<string | null>(null);

  const uploadSources = useMemo(
    () =>
      (sourcesData?.items ?? [])
        .filter((s) => s.spec.type === "upload")
        .map((s) => s.metadata.name),
    [sourcesData],
  );

  const items = useMemo(() => data?.items ?? [], [data]);
  const sources = useMemo(() => {
    const out = new Set<string>();
    for (const it of items) for (const s of it.sources) out.add(s.name);
    return ["all", ...Array.from(out).sort()];
  }, [items]);

  const catChips = useMemo(
    () => categoryFilters((data?.items ?? []).map((e) => resolveCategories(e.categories, e.game ?? ""))),
    [data],
  );

  const catActive = (c: string) => (c === "all" ? catFilter.size === 0 : catFilter.has(c));
  const toggleCat = (c: string) => {
    if (c === "all") {
      setCatFilter(new Set());
      return;
    }
    setCatFilter((prev) => {
      const next = new Set(prev);
      if (next.has(c)) next.delete(c);
      else next.add(c);
      return next;
    });
  };

  const visible = items.filter((e) => {
    if (sourceFilter !== "all" && !e.sources.some((s) => s.name === sourceFilter)) return false;
    if (!matchesAllCategories(e.categories, e.game ?? "", catFilter)) return false;
    if (q && !(e.displayName ?? e.name).toLowerCase().includes(q.toLowerCase())) {
      return false;
    }
    return true;
  });

  const installMutation = useMutation({
    mutationFn: (args: { source: string; module: string; name: string; version: string }) =>
      Modules.install(args, cluster),
    onSuccess: async () => {
      setInstallTarget(null);
      setPageError(null);
      await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
    },
    onError: (err: Error) => setPageError(formatErr(err)),
  });

  const upgradeMutation = useMutation({
    mutationFn: (args: { name: string; version: string }) =>
      Modules.upgrade(args.name, args.version, cluster),
    onSuccess: async () => {
      setPageError(null);
      await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
    },
    onError: (err: Error) => setPageError(formatErr(err)),
  });

  const uninstallMutation = useMutation({
    mutationFn: (name: string) => Modules.uninstall(name, cluster),
    onSuccess: async () => {
      setUninstallTarget(null);
      setPageError(null);
      await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
    },
    onError: (err: Error) => setPageError(formatErr(err)),
  });

  const removeUploadMutation = useMutation({
    mutationFn: (args: { source: string; module: string }) =>
      ModuleSources.removeUpload(args.source, args.module, cluster),
    onSuccess: async () => {
      setRemoveUploadTarget(null);
      setPageError(null);
      await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
    },
    onError: (err: Error) => setPageError(formatErr(err)),
  });

  if (isLoading) return <LoadingCard message="Loading module catalog…" />;
  if (isError) return <ErrorCard message="Failed to load module catalog." onRetry={() => void refetch()} />;

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title="Modules"
        subtitle="Pre-packaged game-server templates pulled from your configured module sources."
        actions={
          <div className="flex items-center gap-2">
            <Button isDisabled={!canManage} onClick={() => setBuildOpen(true)}>
              <Plus className="h-4 w-4" /> Create module
            </Button>
            {uploadSources.length > 0 && (
              <Button variant="outline" isDisabled={!canManage} onPress={() => setUploadOpen(true)}>
                <Upload className="h-4 w-4" /> Upload module
              </Button>
            )}
            <Button variant="outline" onPress={() => setSourcesOpen((open) => !open)} aria-expanded={sourcesOpen}>
              <Settings2 className="h-4 w-4" /> Manage sources
            </Button>
          </div>
        }
      />

      {sourcesOpen ? <ClusterModuleSources cluster={cluster} canManage={canManage} />
        : sourcesError && <ErrorCard message="Failed to load module sources." onRetry={() => void refetchSources()} />}

      <div className="flex flex-wrap items-center gap-3">
        <div className="inline-flex gap-1 rounded-md border border-border bg-card p-1">
          {sources.map((s) => (
            <Button
              key={s}
              onPress={() => setSourceFilter(s)}
              variant="ghost"
              className={cn(
                "px-3 py-1.5 text-xs h-auto",
                sourceFilter === s
                  ? "bg-primary/15 text-primary"
                  : "text-muted hover:text-fg",
              )}
            >
              {s === "all" ? "All sources" : s}
            </Button>
          ))}
        </div>
        <div className="relative min-w-0">
          <div className="flex gap-1 overflow-x-auto rounded-md border border-border bg-card p-1 scrollbar-thin">
            {catChips.map((c) => (
              <Button
                key={c}
                onPress={() => toggleCat(c)}
                variant="ghost"
                className={cn(
                  "shrink-0 px-3 py-1.5 text-xs h-auto transition-colors",
                  catActive(c)
                    ? "bg-primary/15 text-primary"
                    : "text-muted hover:text-fg",
                )}
              >
                {c === "all" ? "All categories" : c}
              </Button>
            ))}
          </div>
          <div className="absolute inset-y-0 right-0 w-14 bg-gradient-to-r from-transparent to-card pointer-events-none rounded-md" />
        </div>
        <div className="relative ml-auto w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
          <Input
            className="pl-9"
            placeholder="Search modules…"
            aria-label="Search modules"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
      </div>

      {pageError && (
        <div className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {pageError}
        </div>
      )}

      <div
        className="grid gap-x-4 gap-y-6 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
        data-testid="modules-grid"
      >
        {visible.map((entry) => (
          <ModuleCard
            cluster={cluster}
            canManage={canManage}
            key={entry.name}
            entry={entry}
            verify={verifyForEntry(entry, sourcesData?.items ?? [])}
            onInstall={setInstallTarget}
            onUpgrade={(e) => {
              if (!e.moduleName || !e.latestVersion) return;
              upgradeMutation.mutate({ name: e.moduleName, version: e.latestVersion });
            }}
            onUninstall={setUninstallTarget}
            onRemoveUpload={setRemoveUploadTarget}
            busy={installMutation.isPending || upgradeMutation.isPending || removeUploadMutation.isPending}
          />
        ))}
        {visible.length === 0 && (
          <div className="col-span-full rounded-lg border border-dashed border-border bg-card/40 p-12 text-center text-sm text-muted">
            {items.length === 0
              ? "No modules in any catalog yet — open Manage sources to add a source or check its sync status."
              : "No modules match the current filter."}
          </div>
        )}
      </div>

      <InstallDialog
        open={!!installTarget}
        onOpenChange={(o) => !o && setInstallTarget(null)}
        entry={installTarget}
        busy={installMutation.isPending}
        onConfirm={async ({ source, version, name }) => {
          if (!installTarget) return;
          await installMutation.mutateAsync({
            source,
            module: installTarget.name,
            name,
            version,
          });
        }}
      />

      <UploadModuleDialog
        cluster={cluster}
        open={uploadOpen}
        onOpenChange={setUploadOpen}
        sources={uploadSources}
        onUploaded={async () => {
          setPageError(null);
          await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
        }}
      />

      <BuildModuleDialog
        cluster={cluster}
        open={buildOpen}
        onOpenChange={setBuildOpen}
        sources={uploadSources}
        onInstalled={async () => {
          setPageError(null);
          await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
        }}
      />

      <ConfirmDialog
        open={!!uninstallTarget}
        onOpenChange={(o) => !o && setUninstallTarget(null)}
        title={`Uninstall ${uninstallTarget?.displayName ?? uninstallTarget?.name ?? ""}?`}
        description={
          <>
            <p>
              Deletes the Module resource and its managed GameTemplate. Game
              servers using this template must be removed first.
            </p>
          </>
        }
        confirmLabel="Uninstall"
        destructive
        busy={uninstallMutation.isPending}
        onConfirm={() => {
          if (!uninstallTarget?.moduleName) return;
          uninstallMutation.mutate(uninstallTarget.moduleName);
        }}
      />

      <ConfirmDialog
        open={!!removeUploadTarget}
        onOpenChange={(o) => !o && setRemoveUploadTarget(null)}
        title={`Remove uploaded module?`}
        description={
          <>
            <p>
              Deletes the uploaded bundle backing the <span className="font-mono">{removeUploadTarget?.displayName ?? removeUploadTarget?.name}</span> catalog entry.
              The entry can be re-uploaded later.
            </p>
          </>
        }
        confirmLabel="Remove upload"
        destructive
        busy={removeUploadMutation.isPending}
        onConfirm={() => {
          if (!removeUploadTarget) return;
          const uploadSource = removeUploadTarget.sources.find((s) => s.type === "upload");
          if (!uploadSource) return;
          removeUploadMutation.mutate({
            source: uploadSource.name,
            module: removeUploadTarget.name,
          });
        }}
      />
    </div>
  );
}

function formatErr(err: Error): string {
  if (err instanceof APIError) {
    return err.body || err.message;
  }
  return err.message;
}
