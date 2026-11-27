// Admin theme sources page (spec 043, T049, design 906199366-1). Lists the
// registries, git repos, HTTP indexes and local folders the API pulls theme
// packages from, with enable, refresh, edit and delete. The official built-in
// source can be disabled and edited but not deleted. Route-gated by
// themes:manage (router/tree.tsx).
import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Card, Chip, Switch } from "@heroui/react";
import { Pencil, Plus, RefreshCcw, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { SettingsNav } from "@/components/ui/SettingsNav";
import { ThemeSourceDialog } from "@/components/themes/ThemeSourceDialog";
import { themeErrorText } from "@/components/themes/useThemeActions";
import { formatThemeMessage, themeSourcesMessages as M } from "@/components/themes/messages";
import { can, useMe } from "@/lib/auth";
import { ThemeSources as ThemeSourcesApi } from "@/lib/endpoints";
import { useOptionalIntl } from "@/lib/i18n/format";
import { formatRelative } from "@/lib/utils";
import type { ThemeSource, ThemeSourceInput, ThemeSourceType } from "@/types";

const SOURCES_KEY = "theme-sources";

const TYPE_LABEL: Record<ThemeSourceType, string> = { oci: "OCI", git: "GIT", http: "HTTP", local: "LOCAL" };

function locationOf(s: ThemeSource): string {
  return s.spec.oci?.url ?? s.spec.git?.url ?? s.spec.http?.url ?? s.spec.local?.path ?? "";
}

// The input body a PUT needs, copied from the listed source with one field changed.
function asInput(s: ThemeSource, patch: Partial<ThemeSourceInput> = {}): ThemeSourceInput {
  return {
    name: s.name,
    type: s.type,
    spec: s.spec,
    verify: s.verify,
    refreshInterval: s.refreshInterval,
    enabled: s.enabled,
    ...patch,
  };
}

export function ThemeSourcesPage() {
  const intl = useOptionalIntl();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: me } = useMe();

  const [pageError, setPageError] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<ThemeSource | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<ThemeSource | null>(null);

  const sourcesQuery = useQuery({
    queryKey: [SOURCES_KEY],
    queryFn: () => ThemeSourcesApi.list(),
  });
  const sources = sourcesQuery.data ?? [];

  const invalidate = () => queryClient.invalidateQueries({ queryKey: [SOURCES_KEY] });

  const save = useMutation({
    mutationFn: (input: ThemeSourceInput) =>
      editing ? ThemeSourcesApi.update(editing.name, input) : ThemeSourcesApi.create(input),
    onSuccess: async () => {
      setDialogOpen(false);
      setDialogError(null);
      setPageError(null);
      await invalidate();
    },
    onError: (err) => setDialogError(themeErrorText(err)),
  });

  const toggle = useMutation({
    mutationFn: (s: ThemeSource) => ThemeSourcesApi.update(s.name, asInput(s, { enabled: !s.enabled })),
    onSuccess: async () => {
      setPageError(null);
      await invalidate();
    },
    onError: (err) => setPageError(themeErrorText(err)),
  });

  const refresh = useMutation({
    mutationFn: (s: ThemeSource) => ThemeSourcesApi.refresh(s.name),
    onSuccess: async () => {
      setPageError(null);
      await invalidate();
    },
    onError: (err) => setPageError(themeErrorText(err)),
  });

  const remove = useMutation({
    mutationFn: (s: ThemeSource) => ThemeSourcesApi.remove(s.name),
    onSuccess: async () => {
      setPendingDelete(null);
      setPageError(null);
      await invalidate();
    },
    onError: (err) => {
      setPendingDelete(null);
      setPageError(themeErrorText(err));
    },
  });

  const openAdd = () => {
    setEditing(null);
    setDialogError(null);
    save.reset();
    setDialogOpen(true);
  };

  const openEdit = (s: ThemeSource) => {
    setEditing(s);
    setDialogError(null);
    save.reset();
    setDialogOpen(true);
  };

  // "5m ago", as in the design; formatRelative owns the clock read.
  const lastSyncText = (iso: string | null): string => formatRelative(iso ?? undefined, intl);

  const statusNode = (s: ThemeSource) => {
    if (s.lastError) {
      return <span className="text-danger">{formatThemeMessage(intl, M.statusFailed, { error: s.lastError })}</span>;
    }
    if (s.lastSyncAt) {
      return <span className="text-success">{intl.formatMessage(M.statusSynced)}</span>;
    }
    return <span className="text-muted">{intl.formatMessage(M.statusNever)}</span>;
  };

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage(M.adminTitle)}
        description={intl.formatMessage(M.adminDescription)}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        <SettingsNav
          active="themeSources"
          showAdminSections={can(me, "config:manage")}
          showThemeSources
          onSelect={(key) => void navigate({ to: "/admin", search: { section: key } })}
        />

        <Card className="space-y-4" data-testid="theme-sources-card">
          <Card.Header className="flex flex-wrap items-start justify-between gap-3 pb-2">
            <div className="min-w-0 space-y-2">
              <div className="font-medium text-base">{intl.formatMessage(M.cardTitle)}</div>
              <div className="text-xs text-muted">{intl.formatMessage(M.cardDescription)}</div>
            </div>
            <Button variant="primary" size="sm" className="text-xs" onPress={openAdd}>
              <Plus className="h-3.5 w-3.5" />
              {intl.formatMessage(M.addSource)}
            </Button>
          </Card.Header>

          <Card.Content className="space-y-4">
            {pageError && (
              <div role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
                {pageError}
              </div>
            )}
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted">
                  <tr className="border-b border-border">
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colName)}</th>
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colType)}</th>
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colStatus)}</th>
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colLastSync)}</th>
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colThemes)}</th>
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colEnabled)}</th>
                    <th className="px-3 py-2 font-normal">{intl.formatMessage(M.colActions)}</th>
                  </tr>
                </thead>
                <tbody>
                  {sources.map((s) => (
                    <tr key={s.name} className="border-b border-border last:border-0" data-testid="theme-source-row">
                      <td className="px-3 py-3">
                        <div className="flex items-center gap-2">
                          <span className="font-medium">{s.name}</span>
                          {s.builtin && (
                            <Chip color="accent" size="sm" variant="soft" className="font-mono text-[10px] uppercase font-normal">
                              {intl.formatMessage(M.builtin)}
                            </Chip>
                          )}
                        </div>
                        <div className="font-mono text-[11px] text-muted">{locationOf(s)}</div>
                      </td>
                      <td className="px-3 py-3 font-mono text-xs">{TYPE_LABEL[s.type]}</td>
                      <td className="px-3 py-3 text-xs">{statusNode(s)}</td>
                      <td className="px-3 py-3 text-xs text-muted">{lastSyncText(s.lastSyncAt)}</td>
                      <td className="px-3 py-3">{s.themeCount}</td>
                      <td className="px-3 py-3">
                        <Switch
                          aria-label={formatThemeMessage(intl, M.toggleFor, { name: s.name })}
                          isSelected={s.enabled}
                          isDisabled={toggle.isPending}
                          onChange={() => toggle.mutate(s)}
                        >
                          <Switch.Content>
                            <Switch.Control>
                              <Switch.Thumb />
                            </Switch.Control>
                          </Switch.Content>
                        </Switch>
                      </td>
                      <td className="px-3 py-3">
                        <div className="flex items-center gap-1">
                          <button
                            type="button"
                            aria-label={formatThemeMessage(intl, M.refreshFor, { name: s.name })}
                            title={intl.formatMessage(M.refreshAction)}
                            disabled={refresh.isPending}
                            onClick={() => refresh.mutate(s)}
                            className="rounded p-1.5 text-muted hover:bg-surface hover:text-fg disabled:opacity-50"
                          >
                            <RefreshCcw className="h-4 w-4" />
                          </button>
                          <button
                            type="button"
                            aria-label={formatThemeMessage(intl, M.editFor, { name: s.name })}
                            title={intl.formatMessage(M.editAction)}
                            onClick={() => openEdit(s)}
                            className="rounded p-1.5 text-muted hover:bg-surface hover:text-fg"
                          >
                            <Pencil className="h-4 w-4" />
                          </button>
                          {!s.builtin && (
                            <button
                              type="button"
                              aria-label={formatThemeMessage(intl, M.deleteFor, { name: s.name })}
                              title={intl.formatMessage(M.deleteAction)}
                              onClick={() => setPendingDelete(s)}
                              className="rounded p-1.5 text-danger hover:bg-danger/10"
                            >
                              <Trash2 className="h-4 w-4" />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="text-xs text-muted">{intl.formatMessage(M.footer)}</p>
          </Card.Content>
        </Card>
      </div>

      <ThemeSourceDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        source={editing}
        busy={save.isPending}
        error={dialogError}
        onSubmit={(input) => save.mutate(input)}
      />

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
        title={pendingDelete ? formatThemeMessage(intl, M.deleteTitle, { name: String(pendingDelete.name) }) : ""}
        description={intl.formatMessage(M.deleteText)}
        confirmLabel={intl.formatMessage(M.deleteConfirm)}
        destructive
        busy={remove.isPending}
        onConfirm={() => {
          if (pendingDelete) remove.mutate(pendingDelete);
        }}
      />
    </div>
  );
}
