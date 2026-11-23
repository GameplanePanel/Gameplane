import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Switch } from "@heroui/react";
import { ErrorBanner } from "@/components/backups/ErrorBanner";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { SecuritySettings } from "@/lib/endpoints";
import { APIError } from "@/lib/api";
import { useMe, can } from "@/lib/auth";
import { useOptionalIntl } from "@/lib/i18n/format";
import { ClusterSection } from "./ClusterSection";

const MISSING_MESSAGE_ID = "server.cluster.security_settings_missing";

// A 409 (or the stable messageId) means the cluster's security ConfigMap is missing.
function isMissingError(err: unknown): boolean {
  return err instanceof APIError && (err.status === 409 || err.messageId === MISSING_MESSAGE_ID);
}

export function SecuritySettingsPanel({ clusterId }: { clusterId: string }) {
  const intl = useOptionalIntl();
  const identity = useMe();
  if (identity.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.clusters.security_loading_permissions", defaultMessage: "Loading permissions…", description: "Cluster security settings: identity loading state" })} />;
  if (identity.isError && (!identity.data || (identity.error instanceof APIError && identity.error.status === 401))) return <ErrorCard message={intl.formatMessage({ id: "web.clusters.security_permissions_error", defaultMessage: "Couldn't load your permissions.", description: "Cluster security settings: identity loading failure" })} onRetry={() => void identity.refetch()} />;
  const title = intl.formatMessage({ id: "web.clusters.security_title", defaultMessage: "Security", description: "Cluster security settings: title of the section card for one cluster" });
  if (!can(identity.data, "config:read")) return <ClusterSection title={title} subtitle={intl.formatMessage({ id: "web.clusters.security_subtitle", defaultMessage: "How game servers on this cluster may run.", description: "Cluster security settings: subtitle under the Security section title" })}><p className="text-sm text-muted">{intl.formatMessage({ id: "web.clusters.security_denied", defaultMessage: "You don't have permission to view this cluster's security settings.", description: "Cluster security settings: access denied when the user cannot read configuration" })}</p></ClusterSection>;
  return <>{identity.isError && <ErrorCard message={intl.formatMessage({ id: "web.clusters.security_permissions_error", defaultMessage: "Couldn't load your permissions.", description: "Cluster security settings: identity loading failure" })} onRetry={() => void identity.refetch()} />}<ClusterSecuritySection key={clusterId} cluster={clusterId} canManage={can(identity.data, "config:manage")} /></>;
}

function ClusterSecuritySection({ cluster, canManage }: { cluster: string; canManage: boolean }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const queryKey = ["security-settings", cluster];

  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => SecuritySettings.get(cluster, signal),
  });
  const save = useMutation({
    mutationFn: (allowRootOverrides: boolean) => SecuritySettings.put(allowRootOverrides, cluster),
    onSuccess: (data) => {
      qc.setQueryData(queryKey, data);
    },
  });

  const title = intl.formatMessage({ id: "web.clusters.security_title", defaultMessage: "Security", description: "Cluster security settings: title of the section card for one cluster" });
  const subtitle = intl.formatMessage({ id: "web.clusters.security_subtitle", defaultMessage: "How game servers on this cluster may run.", description: "Cluster security settings: subtitle under the Security section title" });

  if (query.isPending) return <ClusterSection title={title} subtitle={subtitle}><div className="text-sm text-muted">{intl.formatMessage({ id: "web.clusters.security_loading", defaultMessage: "Loading security settings…", description: "Cluster security settings: loading text while the settings are fetched" })}</div></ClusterSection>;
  if (query.isError && !isMissingError(query.error)) return <ClusterSection title={title} subtitle={subtitle}><ErrorCard message={intl.formatMessage({ id: "web.clusters.security_load_error", defaultMessage: "Couldn't load this cluster's security settings.", description: "Cluster security settings: error shown when the settings cannot be fetched; a retry button appears below it" })} onRetry={() => void query.refetch()} /></ClusterSection>;

  const current = query.data;
  const missing = query.data?.present === false || isMissingError(query.error) || isMissingError(save.error);
  const saveFailure = save.error && !isMissingError(save.error) ? save.error : null;

  return <ClusterSection title={title} subtitle={subtitle}>
    {missing && <Alert status="warning">
      <Alert.Content className="flex flex-col gap-1">
        <Alert.Title className="font-semibold text-sm">{intl.formatMessage({ id: "web.clusters.security_missing_title", defaultMessage: "Security settings missing", description: "Cluster security settings: warning title when the cluster's security ConfigMap is not found" })}</Alert.Title>
        <Alert.Description className="text-sm">{intl.formatMessage({ id: "web.clusters.security_missing_body", defaultMessage: "This cluster's security settings were not found. Run helm upgrade for Gameplane on this cluster, then reload this page.", description: "Cluster security settings: warning body when the security settings are missing; helm upgrade is a command name, usually left untranslated" })}</Alert.Description>
      </Alert.Content>
    </Alert>}
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="text-sm font-medium">{intl.formatMessage({ id: "web.clusters.security_root_label", defaultMessage: "Allow root overrides", description: "Cluster security settings: label of the switch that allows game servers to run as root or the image default user" })}</div>
        <p className="text-xs text-muted">{intl.formatMessage({ id: "web.clusters.security_root_help", defaultMessage: "Lets server owners run a game as root (user or group 0), or with the image's default user. Off by default, set per cluster.", description: "Cluster security settings: help text under the allow root overrides switch" })}</p>
      </div>
      <Switch
        isSelected={current?.allowRootOverrides === true}
        isDisabled={!canManage || save.isPending || missing || !current}
        onChange={(next: boolean) => save.mutate(next)}
        aria-label={intl.formatMessage({ id: "web.clusters.security_root_label", defaultMessage: "Allow root overrides", description: "Cluster security settings: label of the switch that allows game servers to run as root or the image default user" })}
      >
        <Switch.Content>
          <Switch.Control>
            <Switch.Thumb />
          </Switch.Control>
        </Switch.Content>
      </Switch>
    </div>
    <p className="text-xs text-muted">{intl.formatMessage({ id: "web.clusters.security_root_note", defaultMessage: "Turning this off does not stop running servers on this cluster. Stopped servers that hold a root override will not start until the override is cleared or this is turned back on.", description: "Cluster security settings: note explaining that disabling root overrides does not stop running servers but blocks stopped servers with a root override from starting" })}</p>
    {saveFailure && <ErrorBanner err={saveFailure} />}
  </ClusterSection>;
}
