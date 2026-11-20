import { useQuery } from "@tanstack/react-query";
import { useResourceClient, useResourceTarget, resourceKey, useResourcePermissions, resourceCan, type ResourceScope } from "./resourceTarget";
import { APIError } from "./api";
import { backupReadOptions, isAuthorizationError, useBackupAccessKey } from "./backupReadOptions";
import { useOptionalIntl } from "./i18n/format";
import type { BackupDestination } from "@/types";

export function useBackupDestinations(scope?: ResourceScope, explicitPermissions?: readonly string[]) {
  const intl = useOptionalIntl();
  const target = useResourceTarget({ name: "" }, scope ? { ...scope, name: "" } : undefined);
  const client = useResourceClient(target);
  const permissions = useResourcePermissions(explicitPermissions);
  const accessKey = useBackupAccessKey(target, permissions, "backup-destinations");
  const allowed = resourceCan(permissions, "destinations:read");
  const query = useQuery({
    queryKey: [...resourceKey(target, "backup-destinations"), "access", accessKey],
    queryFn: ({ signal }) => client.withSignal(signal).BackupDestinations.list(),
    select: (resp): BackupDestination[] => resp.items,
    ...backupReadOptions(allowed, false),
  });
  const denied = !allowed || isAuthorizationError(query.error);
  return {
    ...query,
    data: denied ? undefined : query.data,
    error: denied ? new APIError(403, intl.formatMessage({ id: "web.lib.destinations.access_denied", defaultMessage: "You do not have permission to view backup destinations in this cluster and namespace.", description: "Backup destination lookup: access denied message for the selected cluster and namespace" })) : query.error,
    isSuccess: !denied && query.isSuccess,
  };
}
