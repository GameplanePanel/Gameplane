import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input as HeroInput } from "@heroui/react";
import { Archive, Plus, Trash2 } from "lucide-react";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ErrorBanner } from "@/components/backups/ErrorBanner";
import { createResourceClient } from "@/lib/endpoints";
import { APIError } from "@/lib/api";
import { useMe, can } from "@/lib/auth";
import { formatRelative } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { ClusterSection, ClusterField } from "./ClusterSection";

// Existing destination requests use the API's default workload namespace.
const DESTINATION_NAMESPACE = "gameplane-games";

export function BackupDestinationsPanel({ clusterId }: { clusterId: string }) {
  const intl = useOptionalIntl();
  const identity = useMe();
  if (identity.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.cluster_configuration.loading_permissions", defaultMessage: "Loading permissions…", description: "Cluster configuration: identity loading state" })} />;
  if (identity.isError && (!identity.data || (identity.error instanceof APIError && identity.error.status === 401))) return <ErrorCard message={intl.formatMessage({ id: "web.cluster_configuration.permissions_error", defaultMessage: "Couldn't load your permissions.", description: "Cluster configuration: identity loading failure" })} onRetry={() => void identity.refetch()} />;
  if (!can(identity.data, "destinations:read", DESTINATION_NAMESPACE, clusterId)) return <ClusterSection title={intl.formatMessage({ id: "web.routes.admin_settings.backup_destinations", defaultMessage: "Backup destinations", description: "Admin settings: title of the backup destinations section card for one cluster; keep it short" })}><p className="text-sm text-muted">{intl.formatMessage({ id: "web.cluster_configuration.destinations_denied", defaultMessage: "You don't have permission to view backup destinations on this cluster.", description: "Cluster configuration: backup destination access denied" })}</p></ClusterSection>;
  return <>{identity.isError && <ErrorCard message={intl.formatMessage({ id: "web.cluster_configuration.permissions_error", defaultMessage: "Couldn't load your permissions.", description: "Cluster configuration: identity loading failure" })} onRetry={() => void identity.refetch()} />}<ClusterBackupDestSection key={clusterId} cluster={clusterId} canManage={can(identity.data, "destinations:manage", DESTINATION_NAMESPACE, clusterId)} /></>;
}

function ClusterBackupDestSection({ cluster, canManage }: { cluster: string; canManage: boolean }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { BackupDestinations } = createResourceClient({ cluster, namespace: DESTINATION_NAMESPACE });
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["backup-destinations", cluster, DESTINATION_NAMESPACE],
    queryFn: ({ signal }) => createResourceClient({ cluster, namespace: DESTINATION_NAMESPACE }, signal).BackupDestinations.list(),
  });
  const remove = useMutation({
    mutationFn: (name: string) => BackupDestinations.remove(name),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["backup-destinations", cluster, DESTINATION_NAMESPACE] });
      setDeleting(null);
    },
  });

  const items = list.data?.items ?? [];

  return (
    <ClusterSection
      title={intl.formatMessage({ id: "web.routes.admin_settings.backup_destinations", defaultMessage: "Backup destinations", description: "Admin settings: title of the backup destinations section card for one cluster; keep it short" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.restic_repositories_for_snapshots", defaultMessage: "Restic repositories for snapshots. Stored as labelled Kubernetes Secrets in the configured namespace.", description: "Admin settings: subtitle under Backup destinations: Restic repositories for snapshots, stored as labelled Kubernetes Secrets (Restic: technical term, usually left untranslated)" })}
      footer={canManage &&
        <Button onPress={() => setAdding(true)} isDisabled={adding}>
          <Plus className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.admin_settings.add_destination", defaultMessage: "Add destination", description: "Admin settings: button that opens the form to add a backup destination, and the heading of that form; keep it short" })}</Button>
      }
    >
      {list.isLoading && <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.loading_destinations", defaultMessage: "Loading destinations…", description: "Admin settings: loading text in the backup destinations card while the list is fetched" })}</div>}
      {list.isError && <ErrorBanner err={list.error} />}
      {!list.isLoading && !list.isError && items.length === 0 && !adding && (
        <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.no_backup_destinations_configured_add", defaultMessage: "No backup destinations configured. Add one to enable snapshots.", description: "Admin settings: empty-state text in the backup destinations card when none exist, asking the user to add one to enable snapshots" })}</div>
      )}
      {adding && canManage && <NewDestinationForm cluster={cluster} onClose={() => setAdding(false)} />}
      <ul className="divide-y divide-border">
        {items.map((d) => (
          <li key={d.name} className="flex flex-wrap items-center gap-3 py-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-md bg-surface">
              <Archive className="h-4 w-4 text-muted" />
            </div>
            <div className="min-w-0 flex-1 basis-32">
              <div className="break-all text-sm">{d.name}</div>
              <div className="truncate font-mono text-xs text-muted">{d.url}</div>
            </div>
            <span className="rounded bg-success/15 px-2 py-0.5 text-[10px] font-mono uppercase text-success">
              {d.hasPassword ? intl.formatMessage({ id: "web.routes.admin_settings.configured_lower", defaultMessage: "configured", description: "Admin settings: badge on a backup destination whose password is stored" }) : intl.formatMessage({ id: "web.routes.admin_settings.no_password", defaultMessage: "no-password", description: "Admin settings: badge on a backup destination with no stored password" })}
            </span>
            {d.createdAt && (
              <span className="hidden text-[11px] text-muted sm:inline">{formatRelative(d.createdAt, intl)}</span>
            )}
            {canManage && <button
              type="button"
              aria-label={intl.formatMessage({ id: "web.routes.admin_settings.delete_destination", defaultMessage: "Delete {name}", description: "Admin settings: aria-label of the button that deletes a backup destination; {name} is the destination name" }, { name: d.name })}
              onClick={() => { remove.reset(); setDeleting(d.name); }}
              className="rounded p-1 text-muted hover:bg-surface/60 hover:text-danger"
            >
              <Trash2 className="h-4 w-4" />
            </button>}
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => { if (!open && !remove.isPending) { setDeleting(null); remove.reset(); } }}
        title={intl.formatMessage({ id: "web.routes.admin_settings.delete_backup_destination", defaultMessage: "Delete backup destination?", description: "Admin settings: title of the confirmation dialog that asks whether to delete a backup destination" })}
        description={
          <>
            {remove.error && <ErrorBanner err={remove.error} />}
            <p>{intl.formatMessage({ id: "web.routes.admin_settings.the_repository_at_this_destination_will", defaultMessage: "The repository at this destination will", description: "Admin settings: opening words of the delete dialog text, followed by the emphasised word not and the rest of the sentence" })} <strong>{intl.formatMessage({ id: "web.routes.admin_settings.not", defaultMessage: "not", description: "Admin settings: the word not, shown in bold in the delete dialog text to stress the repository is not erased" })}</strong> {intl.formatMessage({ id: "web.routes.admin_settings.be_erased_only_the_credentials", defaultMessage: "be erased — only the credentials Gameplane uses to reach it. Existing backups remain intact; new backups against this destination will fail until it's re-added.", description: "Admin settings: end of the delete dialog text: only Gameplane's credentials for the destination are removed, existing backups stay and new backups fail" })}</p>
            {deleting && (
              <p className="pt-2">{intl.formatMessage({ id: "web.routes.admin_settings.type", defaultMessage: "Type", description: "Admin settings: first word of the line in the delete dialog asking the user to type the destination name to confirm" })} <span className="font-mono">{deleting}</span> {intl.formatMessage({ id: "web.routes.admin_settings.to_confirm", defaultMessage: "to confirm.", description: "Admin settings: last words of the line asking the user to type the destination name to confirm; the name is shown in monospace before them" })}</p>
            )}
          </>
        }
        confirmPhrase={deleting ?? undefined}
        confirmLabel={intl.formatMessage({ id: "web.routes.admin_settings.delete", defaultMessage: "Delete", description: "Admin settings: confirm button of the delete backup destination dialog; keep it short" })}
        destructive
        busy={remove.isPending}
        onConfirm={() => canManage && deleting && remove.mutate(deleting)}
      />
    </ClusterSection>
  );
}

function NewDestinationForm({ cluster, onClose }: { cluster: string; onClose: () => void }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { BackupDestinations } = createResourceClient({ cluster, namespace: DESTINATION_NAMESPACE });
  const [form, setForm] = useState({ name: "", url: "", password: "" });
  const create = useMutation({
    mutationFn: () => BackupDestinations.upsert(form),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["backup-destinations", cluster, DESTINATION_NAMESPACE] });
      onClose();
    },
  });
  const valid =
    /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/.test(form.name) &&
    form.url.length > 0 &&
    form.password.length > 0;
  return (
    <div className="space-y-3 rounded-md border border-border bg-surface/30 p-4">
      <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.add_destination", defaultMessage: "Add destination", description: "Admin settings: button that opens the form to add a backup destination, and the heading of that form; keep it short" })}</div>
      <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.restic_url_hint", defaultMessage: "Stored as a Secret labelled {secret}. Restic URL formats: {s3}, {b2}, {azure}, etc.", description: "Admin settings: hint under the backup destination form; {secret} is the Secret label, {s3}, {b2} and {azure} are example Restic URL formats" }, { secret: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.gameplane_local_backup_destination_true", defaultMessage: "gameplane.local/backup-destination=true", description: "Admin settings: the Secret label example in the backup destination hint" })}</span>, s3: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.s3_host_bucket", defaultMessage: "s3:host/bucket", description: "Admin settings: example Restic URL format for S3" })}</span>, b2: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.b2_bucket", defaultMessage: "b2:bucket", description: "Admin settings: example Restic URL format for Backblaze B2" })}</span>, azure: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.azure_account_container", defaultMessage: "azure:account/container", description: "Admin settings: example Restic URL format for Azure" })}</span> })}</p>
      <div className="grid gap-3 md:grid-cols-2">
        <ClusterField label={intl.formatMessage({ id: "web.routes.admin_settings.name_dns_label", defaultMessage: "Name (DNS label)", description: "Admin settings: label of the destination name field, which must be a DNS label of lowercase letters, digits and dashes (DNS label: technical term, usually left untranslated)" })}>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.gameplane_backup_repo", defaultMessage: "gameplane-backup-repo", description: "Admin settings: grey example name inside the destination name field, shown only while it is empty" })}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </ClusterField>
        <ClusterField label={intl.formatMessage({ id: "web.routes.admin_settings.restic_url", defaultMessage: "Restic URL", description: "Admin settings: label of the field for the Restic repository URL (Restic: technical term, usually left untranslated)" })}>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.s3_s3_example_com_gameplane_bucket", defaultMessage: "s3:s3.example.com/gameplane-bucket", description: "Admin settings: grey example Restic URL inside the URL field, pointing at an S3 bucket (S3: technical term, usually left untranslated)" })}
            value={form.url}
            onChange={(e) => setForm({ ...form, url: e.target.value })}
          />
        </ClusterField>
        <ClusterField label={intl.formatMessage({ id: "web.routes.admin_settings.repository_password", defaultMessage: "Repository password", description: "Admin settings: label of the password field for the Restic backup repository" })}>
          <HeroInput
            type="password"
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.strong_unique_passphrase", defaultMessage: "Strong, unique passphrase", description: "Admin settings: grey hint inside the repository password field, suggesting a strong unique passphrase; shown only while it is empty" })}
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
          />
        </ClusterField>
      </div>
      {create.error && <ErrorBanner err={create.error} />}
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>{intl.formatMessage({ id: "web.routes.admin_settings.cancel", defaultMessage: "Cancel", description: "Admin settings: button that closes an add or set form without saving; keep it short" })}</Button>
        <Button
          onPress={() => create.mutate()}
          isDisabled={!valid || create.isPending}
        >
          {create.isPending ? intl.formatMessage({ id: "web.routes.admin_settings.saving_destination", defaultMessage: "Saving…", description: "Admin settings: button label while a backup destination is being saved" }) : intl.formatMessage({ id: "web.routes.admin_settings.save_destination", defaultMessage: "Save destination", description: "Admin settings: button label that saves a new backup destination" })}
        </Button>
      </div>
    </div>
  );
}

