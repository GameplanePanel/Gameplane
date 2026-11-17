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
    retryOnMount: false,
    refetchInterval: (query: { state: { error: Error | null } }) =>
      allowed && !isAuthorizationError(query.state.error) ? interval : false,
  };
}
