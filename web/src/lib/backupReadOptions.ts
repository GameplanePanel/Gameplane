import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ResourceTarget } from "./resourceTarget";
import { APIError } from "@/lib/api";

export function isAuthorizationError(error: unknown): boolean {
  return error instanceof APIError && (error.status === 401 || error.status === 403);
}

// An authorization failure is definitive for this access snapshot. Disabling
// its observer also stops focus, reconnect, mount and invalidation refetches.
// The caller includes the scoped permissions in the key, so changed access
// starts a fresh query while ordinary rerenders preserve the denial.
export function backupReadOptions(allowed: boolean, interval: number | false) {
  return {
    enabled: (query: { state: { error: Error | null } }) => allowed && !isAuthorizationError(query.state.error),
    retry: (failureCount: number, error: Error) => !isAuthorizationError(error) && failureCount < 3,
    refetchInterval: (query: { state: { error: Error | null } }) =>
      allowed && !isAuthorizationError(query.state.error) ? interval : false,
  };
}

// Shared with standalone backup drawers: permission restoration must also work
// when the server Backups tab is not mounted and the cache retains an old denial.
// Opening a surface again permits one fresh read; rerenders keep a denial stopped.
export function useBackupAccessKey(target: ResourceTarget, permissions: readonly string[], kind: "collections" | "backup" | "backup-destinations" = "collections"): string {
  const qc = useQueryClient();
  const accessKey = [...permissions].sort().join(",");
  const scopeKey = JSON.stringify([target.cluster, target.namespace, accessKey, kind, kind === "backup" ? target.name : ""]);
  const previousAccess = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previousAccess.current === scopeKey) return;
    previousAccess.current = scopeKey;
    void qc.resetQueries({
      queryKey: ["resource", target.cluster, target.namespace ?? ""],
      predicate: (query) => isAuthorizationError(query.state.error) && (
        kind === "backup"
          ? query.queryKey[3] === target.name && query.queryKey[5] === "backup"
          : query.queryKey[3] === "collection" && (
            kind === "backup-destinations"
              ? query.queryKey[4] === kind
              : ["backups", "schedules", "restores"].includes(String(query.queryKey[4]))
          )
      ),
    });
  }, [scopeKey, qc, target.cluster, target.namespace, target.name, kind]);
  return accessKey;
}
