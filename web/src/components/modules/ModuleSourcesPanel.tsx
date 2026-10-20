import { ModuleTarget } from "@/components/ModuleTarget";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  CircleAlert,
  Circle,
  Pencil,
  Plus,
  RefreshCcw,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { Button, Card } from "@heroui/react";

import { APIError } from "@/lib/api";
import { ModuleSources } from "@/lib/endpoints";
import { formatRelative } from "@/lib/utils";
import { verifyMode } from "@/lib/verify";
import type { ModuleSource, ModuleSourceSpec } from "@/types";
import { SourceDialog } from "./SourceDialog";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { englishIntl, useOptionalIntl } from "@/lib/i18n/format";
import type { IntlShape } from "react-intl";

// ModuleSourcesPanel lists every ModuleSource and lets admins add,
// edit, and remove them. The same sources can equally be declared via
// Helm values or `kubectl apply` — the dashboard just writes the CRs.
export function ModuleSourcesPanel() {
  const intl = useOptionalIntl();
  return <ModuleTarget loadingMessage={intl.formatMessage({ id: "web.components.modules.module_sources_panel.loading_sources", defaultMessage: "Loading module sources\u2026", description: "Module sources panel: loading message while module sources load" })}>{(cluster, canManage) => <ClusterModuleSources key={cluster} cluster={cluster} canManage={canManage} />}</ModuleTarget>;
}

export function ClusterModuleSources({ cluster, canManage }: { cluster: string; canManage: boolean }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["module-sources", cluster],
    queryFn: () => ModuleSources.list(cluster),
    refetchInterval: 10_000,
  });

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<ModuleSource | null>(null);
  const [panelError, setPanelError] = useState<string | null>(null);

  const invalidate = async () => {
    setPanelError(null);
    await qc.invalidateQueries({ queryKey: ["module-sources", cluster] });
    await qc.invalidateQueries({ queryKey: ["modules-catalog", cluster] });
  };

  const saveMutation = useMutation({
    mutationFn: ({ name, spec }: { name: string; spec: ModuleSourceSpec }) =>
      editTarget ? ModuleSources.update(name, spec, cluster) : ModuleSources.create(name, spec, cluster),
    onSuccess: async () => {
      setDialogOpen(false);
      await invalidate();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (name: string) => ModuleSources.remove(name, cluster),
    onSuccess: invalidate,
    onError: (err: Error) =>
      setPanelError(err instanceof APIError ? err.body || err.message : err.message),
  });

  if (isLoading) {
    return <LoadingCard message={intl.formatMessage({ id: "web.components.modules.module_sources_panel.loading_module_sources", defaultMessage: "Loading module sources…", description: "Module sources panel: loading text shown while the list of module sources is being fetched" })} />;
  }
  if (isError && !data) {
    return <ErrorCard message={intl.formatMessage({ id: "web.components.modules.module_sources_panel.failed_to_load_module_sources", defaultMessage: "Failed to load module sources.", description: "Module sources panel: error card message shown when the module sources cannot be loaded; the card has a retry button" })} onRetry={() => void refetch()} />;
  }

  const sources = data?.items ?? [];

  return (
    <Card className="p-5 space-y-3">
      {isError && <ErrorCard message={intl.formatMessage({ id: "web.components.modules.module_sources_panel.failed_to_load_module_sources", defaultMessage: "Failed to load module sources.", description: "Module sources panel: error card message shown when the module sources cannot be loaded; the card has a retry button" })} onRetry={() => void refetch()} />}
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="font-medium">{intl.formatMessage({ id: "web.components.modules.module_sources_panel.module_sources", defaultMessage: "Module sources", description: "Module sources panel: heading of the panel that lists the sources module bundles are pulled from" })}</div>
          <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.components.modules.module_sources_panel.intro", defaultMessage: "Registries, git repositories, archives, and uploads the operator pulls module bundles from. Equivalent to applying <b>ModuleSource</b> resources directly.", description: "Module sources panel: explanation under the heading saying sources are registries, git repositories, archives or uploads, the same as applying ModuleSource resources directly; the <b> tag wraps the resource name ModuleSource, a technical term usually left untranslated, and must be kept around it" }, { b: (chunks) => <code className="font-mono">{chunks}</code> })}</div>
        </div>
        <Button
          size="sm"
          isDisabled={!canManage}
          variant="primary"
          onPress={() => {
            setEditTarget(null);
            setDialogOpen(true);
          }}
        >
          <Plus className="h-3.5 w-3.5" /> {intl.formatMessage({ id: "web.components.modules.module_sources_panel.add_source", defaultMessage: "Add source", description: "Module sources panel: button that opens the dialog to add a new module source; keep it short" })}</Button>
      </div>

      {panelError && (
        <div className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {panelError}
        </div>
      )}

      {sources.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-center text-sm text-muted">{intl.formatMessage({ id: "web.components.modules.module_sources_panel.empty", defaultMessage: "No <b>ModuleSource</b> resources configured.", description: "Module sources panel: empty-state sentence shown when no module sources are configured; the <b> tag wraps the resource name ModuleSource, a technical term usually left untranslated, and must be kept around it" }, { b: (chunks) => <code className="font-mono">{chunks}</code> })}</div>
      ) : (
        <ul className="divide-y divide-border">
          {sources.map((s) => (
            <SourceRow
              canManage={canManage}
              key={s.metadata.name}
              source={s}
              onEdit={() => {
                setEditTarget(s);
                setDialogOpen(true);
              }}
              onDelete={() => deleteMutation.mutate(s.metadata.name)}
              deleting={deleteMutation.isPending}
            />
          ))}
        </ul>
      )}

      <SourceDialog
        open={dialogOpen}
        onOpenChange={(open) => {
          setDialogOpen(open);
          if (!open) setEditTarget(null);
        }}
        source={editTarget}
        onConfirm={(args) => saveMutation.mutateAsync(args).then(() => undefined)}
        busy={saveMutation.isPending}
      />
    </Card>
  );
}

// sourceLocation renders the human-meaningful "where" of a source,
// which lives in a different nested field per source type.
export function sourceLocation(spec: ModuleSource["spec"], intl: IntlShape = englishIntl): string {
  switch (spec.type ?? "oci") {
    case "git":
      return `${spec.git?.url ?? ""}${spec.git?.ref ? `@${spec.git.ref}` : ""}`;
    case "http":
      return spec.http?.url ?? "";
    case "local":
      return spec.local?.path ? `local:${spec.local.path}` : intl.formatMessage({ id: "web.components.modules.module_sources_panel.local_location", defaultMessage: "local", description: "Module sources panel: location shown for a local source that has no path" });
    case "upload":
      return intl.formatMessage({ id: "web.components.modules.module_sources_panel.uploaded_bundles", defaultMessage: "uploaded bundles", description: "Module sources panel: location shown for a source whose modules are uploaded bundles" });
    default:
      return spec.oci?.url ?? "";
  }
}

function SourceRow({
  canManage,
  source,
  onEdit,
  onDelete,
  deleting,
}: {
  canManage: boolean;
  source: ModuleSource;
  onEdit: () => void;
  onDelete: () => void;
  deleting: boolean;
}) {
  const intl = useOptionalIntl();
  // Three sync states: healthy (Synced=True), serving a stale catalog after
  // a transient failure (Synced=False but Ready=True/ServingStaleCatalog),
  // and unreachable (Synced=False, never indexed). Stale is a warning, not a
  // hard failure — the cached catalog is still being served.
  const conditions = source.status?.conditions ?? [];
  const synced = conditions.find((c) => c.type === "Synced");
  const ready = conditions.find((c) => c.type === "Ready");
  const ok = synced?.status === "True";
  const stale = !ok && ready?.status === "True" && ready.reason === "ServingStaleCatalog";
  const Icon = ok ? CheckCircle2 : stale ? AlertTriangle : synced ? CircleAlert : Circle;
  const tone = ok ? "text-success" : stale ? "text-warning" : synced ? "text-danger" : "text-muted";
  const vmode = verifyMode(source.spec.verify);
  const refresh = source.spec.refreshInterval ?? "1h";
  const insecure = source.spec.oci?.insecure ?? source.spec.http?.insecure ?? false;
  const moduleCount =
    source.status?.modules?.length ?? source.spec.oci?.modules.length ?? 0;

  return (
    <li className="flex items-center gap-3 py-3">
      <Icon className={`h-4 w-4 ${tone}`} aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="font-medium text-sm text-fg">
          {source.metadata.name}
          <span className="ml-2 rounded bg-card px-1.5 py-0.5 font-mono text-[10px] uppercase text-muted">
            {source.spec.type ?? intl.formatMessage({ id: "web.components.modules.module_sources_panel.type_oci", defaultMessage: "oci", description: "Module sources panel: fallback source type when the source sets none" })}
          </span>
          {vmode !== "none" && (
            <span className="ml-2 inline-flex items-center gap-1 rounded bg-success/15 px-1.5 py-0.5 align-middle font-mono text-[10px] uppercase text-success">
              <ShieldCheck className="h-3 w-3" />
              {vmode}
            </span>
          )}
        </div>
        <div className="truncate text-xs text-muted">
          <span className="font-mono">{sourceLocation(source.spec, intl)}</span>
          {insecure && (
            <span className="ml-2 rounded bg-warning/15 px-1.5 py-0.5 font-mono text-[10px] text-warning">{intl.formatMessage({ id: "web.components.modules.module_sources_panel.insecure", defaultMessage: "insecure", description: "Module sources panel: small warning badge on a source row marking a source that allows plain HTTP; keep it short" })}</span>
          )}
        </div>
      </div>
      <div className="text-right text-xs text-muted">
        <div className="flex items-center justify-end gap-1">
          <RefreshCcw className="h-3 w-3" />
          <span className="font-mono">{refresh}</span>
        </div>
        <div>{intl.formatMessage({ id: "web.components.modules.module_sources_panel.modulecount_module", defaultMessage: "{moduleCount, plural, one {# module} other {# modules}}", description: "Module sources panel: module count in a source card; {moduleCount} is the number of modules" }, { moduleCount: moduleCount })}
          {source.status?.lastSync && (
            <> {intl.formatMessage({ id: "web.components.modules.module_sources_panel.synced", defaultMessage: "· synced", description: "Module sources panel: text after the module count on a source row, followed by the time of the last sync in relative time; the leading dot is part of the text" })} {formatRelative(source.status.lastSync, intl)}</>
          )}
        </div>
        {stale && <div className="text-warning">{intl.formatMessage({ id: "web.components.modules.module_sources_panel.serving_stale_catalog", defaultMessage: "serving stale catalog", description: "Module sources panel: warning text on a source row shown when the source is still serving an old catalog of modules" })}</div>}
      </div>
      <div className="flex items-center gap-1">
        <Button size="sm" variant="ghost" isDisabled={!canManage} onPress={onEdit} aria-label={intl.formatMessage({ id: "web.components.modules.module_sources_panel.edit_source_aria", defaultMessage: "Edit {name}", description: "Module sources panel: aria-label of the edit button; {name} is the source name" }, { name: source.metadata.name })}>
          <Pencil className="h-3.5 w-3.5" />
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onPress={onDelete}
          isDisabled={deleting || !canManage}
          aria-label={intl.formatMessage({ id: "web.components.modules.module_sources_panel.delete_source_aria", defaultMessage: "Delete {name}", description: "Module sources panel: aria-label of the delete button; {name} is the source name" }, { name: source.metadata.name })}
        >
          <Trash2 className="h-3.5 w-3.5 text-danger" />
        </Button>
      </div>
    </li>
  );
}
