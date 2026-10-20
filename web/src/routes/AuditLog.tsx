import { useMemo, useState, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQuery, type UseQueryResult } from "@tanstack/react-query";
import { Download, RefreshCw, ShieldCheck } from "lucide-react";
import type { AuditEvent, AuditVerifyResult } from "@/types";
import { Audit, type AuditExportFilter } from "@/lib/endpoints";
import { Button, Card, Input, Chip, Table, Tabs, Tab as TabComponent } from "@heroui/react";
import { PageHeader } from "@/components/ui/PageHeader";
import { AuditIntegrityBanner } from "@/components/ui/AuditIntegrityBanner";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { cn, formatRelative } from "@/lib/utils";
import { englishIntl, useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

const PAGE_SIZE = 100;

type StatusClass = "all" | "2xx" | "4xx" | "5xx";
type MethodFilter = "all" | "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export function AuditLogPage() {
  const intl = useOptionalIntl();
  const [statusClass, setStatusClass] = useState<StatusClass>("all");
  const [methodFilter, setMethodFilter] = useState<MethodFilter>("all");
  const [actorQ, setActorQ] = useState("");

  const verifyQuery = useQuery({
    queryKey: ["audit-verify"],
    queryFn: () => Audit.verify(),
  });

  const query = useInfiniteQuery({
    queryKey: ["audit"],
    queryFn: ({ pageParam }: { pageParam: number }) =>
      Audit.page(PAGE_SIZE, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) =>
      last.length === PAGE_SIZE ? last[last.length - 1].id : undefined,
  });

  const exportMutation = useMutation({
    mutationFn: (filter: AuditExportFilter) => Audit.exportCsv(filter),
    onSuccess: (blob) => {
      const filename = "gameplane-audit-" + new Date().toISOString().replace(/[:.]/g, "-") + ".csv";
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      a.click();
      URL.revokeObjectURL(url);
    },
  });

  const all: AuditEvent[] = useMemo(
    () => query.data?.pages.flat() ?? [],
    [query.data],
  );

  const filtered = useMemo(() => {
    const a = actorQ.trim().toLowerCase();
    return all.filter((e) => {
      if (statusClass !== "all" && statusBucket(e.status) !== statusClass) return false;
      if (methodFilter !== "all" && e.method !== methodFilter) return false;
      if (a && !e.actor.toLowerCase().includes(a)) return false;
      return true;
    });
  }, [all, statusClass, methodFilter, actorQ]);

  const totals = useMemo<Record<StatusClass, number>>(() => {
    const r: Record<StatusClass, number> = { all: all.length, "2xx": 0, "4xx": 0, "5xx": 0 };
    for (const e of all) {
      const b = statusBucket(e.status);
      if (b !== "other") r[b]++;
    }
    return r;
  }, [all]);

  return (
    <div className="space-y-5 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.audit_log.audit_log", defaultMessage: "Audit log", description: "Audit log: page title at the top of the audit log screen; keep it short" })}
        description={intl.formatMessage({ id: "web.routes.audit_log.mutating_control_plane_requests_newest", defaultMessage: "Mutating control-plane requests, newest first.", description: "Audit log: description under the title saying the list holds mutating control-plane requests, newest first" })}
        actions={
          <>
            <Button
              variant="outline"
              onPress={() => {
                const filter: AuditExportFilter = {};
                if (actorQ.trim()) filter.actor = actorQ.trim();
                if (methodFilter !== "all") filter.method = methodFilter;
                if (statusClass !== "all") filter.status = statusClass;
                exportMutation.mutate(filter);
              }}
              isDisabled={exportMutation.isPending}
            >
              <Download className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.audit_log.export_csv", defaultMessage: "Export CSV", description: "Audit log: button that downloads the currently filtered audit events as a CSV file; keep it short" })}</Button>
            <Button
              onPress={() => void query.refetch()}
              isDisabled={query.isFetching && !query.isFetchingNextPage}
            >
              <RefreshCw
                className={cn(
                  "h-4 w-4",
                  query.isFetching && !query.isFetchingNextPage && "animate-spin",
                )}
              />{intl.formatMessage({ id: "web.routes.audit_log.refresh", defaultMessage: "Refresh", description: "Audit log: button that reloads the audit events; keep it short" })}</Button>
          </>
        }
      />

      {renderIntegrityBanner(verifyQuery, intl)}

      {exportMutation.error && (
        <ErrorBanner err={exportMutation.error} onDismiss={() => exportMutation.reset()} />
      )}

      <div className="flex flex-wrap items-center gap-3">
        <Tabs selectedKey={statusClass} onSelectionChange={(key) => setStatusClass(key as StatusClass)} variant="secondary" aria-label={intl.formatMessage({ id: "web.routes.audit_log.status_filter", defaultMessage: "Status filter", description: "Audit log: accessible name of the status tabs that filter the audit events by status" })}>
          <Tabs.List className="w-fit">
            {(["all", "2xx", "4xx", "5xx"] as StatusClass[]).map((s) => (
              <TabComponent key={s} id={s} className="text-xs">
                {intl.formatMessage({ id: "web.routes.audit_log.status_tab", defaultMessage: "{label} · {count}", description: "Audit log: status filter tab; {label} is the filter name, {count} is the number of events in it" }, { label: labelFor(s, intl), count: totals[s] ?? 0 })}
              </TabComponent>
            ))}
          </Tabs.List>
        </Tabs>

        <select
          value={methodFilter}
          onChange={(e) => setMethodFilter(e.target.value as MethodFilter)}
          className="h-9 rounded-md border border-border bg-surface px-2 text-sm text-foreground"
        >
          <option value="all">{intl.formatMessage({ id: "web.routes.audit_log.all_methods", defaultMessage: "All methods", description: "Audit log: first option in the method dropdown, meaning no method filter; keep it short" })}</option>
          {(["GET", "POST", "PUT", "PATCH", "DELETE"] as const).map((m) => (
            <option key={m} value={m}>{m}</option>
          ))}
        </select>

        <Input
          placeholder={intl.formatMessage({ id: "web.routes.audit_log.filter_by_actor", defaultMessage: "Filter by actor…", description: "Audit log: grey placeholder of the search field that filters events by the actor who made the request" })}
          value={actorQ}
          onChange={(e) => setActorQ(e.target.value)}
          className="w-64"
          variant="primary"
        />

        <div className="ml-auto text-xs text-muted">{intl.formatMessage({ id: "web.routes.audit_log.length_of_length2_loaded", defaultMessage: "{length} of {length2} loaded", description: "Audit log: sentence with length, length2 in div" }, { length: filtered.length, length2: all.length })}{query.hasNextPage && intl.formatMessage({ id: "web.routes.audit_log.more_available", defaultMessage: " · more available", description: "Audit log: suffix after the loaded count when older events can still be loaded" })}
        </div>
      </div>

      <Card className="overflow-hidden p-0">
        <Table.Root className="bg-transparent text-xs">
          <Table.ScrollContainer className="max-h-[70vh]">
            <Table.Content aria-label={intl.formatMessage({ id: "web.routes.audit_log.audit_log", defaultMessage: "Audit log", description: "Audit log: page title at the top of the audit log screen; keep it short" })} keyboardNavigationBehavior="arrow">
              <Table.Header>
                <Table.Column id="time" isRowHeader className="w-40">{intl.formatMessage({ id: "web.routes.audit_log.time", defaultMessage: "Time", description: "Audit log: column heading for when each event happened; keep it short" })}</Table.Column>
                <Table.Column id="actor" className="w-36">{intl.formatMessage({ id: "web.routes.audit_log.actor", defaultMessage: "Actor", description: "Audit log: column heading for the user who made each request; keep it short" })}</Table.Column>
                <Table.Column id="action">{intl.formatMessage({ id: "web.routes.audit_log.action", defaultMessage: "Action", description: "Audit log: column heading for the action performed in each event; keep it short" })}</Table.Column>
                <Table.Column id="method" className="w-20">{intl.formatMessage({ id: "web.routes.audit_log.method", defaultMessage: "Method", description: "Audit log: column heading for the HTTP method of each request; keep it short" })}</Table.Column>
                <Table.Column id="access" className="w-24">{intl.formatMessage({ id: "web.routes.audit_log.access", defaultMessage: "Access", description: "Audit log: column heading for the access column of each event; keep it short" })}</Table.Column>
                <Table.Column id="ip" className="w-32">{intl.formatMessage({ id: "web.routes.audit_log.ip", defaultMessage: "IP", description: "Audit log: column heading for the IP address of each request; keep it short" })}</Table.Column>
              </Table.Header>
              <Table.Body
                renderEmptyState={() =>
                  <span>
                    {all.length === 0
                      ? intl.formatMessage({ id: "web.routes.audit_log.no_audit_events_yet", defaultMessage: "No audit events yet.", description: "Audit log: empty state when no events have been recorded" })
                      : intl.formatMessage({ id: "web.routes.audit_log.no_events_match_the_active_filters", defaultMessage: "No events match the active filters.", description: "Audit log: empty state when the active filters hide every event" })}
                  </span>
                }
              >
                {filtered.map((e) => {
                  const access = accessOutcome(e.status, intl);
                  return (
                    <Table.Row key={e.id}>
                      <Table.Cell className="text-muted">
                        <span title={e.ts}>{formatRelative(e.ts, intl)}</span>
                      </Table.Cell>
                      <Table.Cell>{e.actor}</Table.Cell>
                      <Table.Cell>
                        <span
                          className="block max-w-[480px] truncate"
                          title={intl.formatMessage({ id: "web.routes.audit_log.method_and_path", defaultMessage: "{method} {path}", description: "Audit log: tooltip with the raw HTTP method and request path; {method} is the method, {path} the path" }, { method: e.method, path: e.path })}
                        >
                          {auditAction(e, intl)}
                        </span>
                      </Table.Cell>
                      <Table.Cell>
                        <MethodPill method={e.method} />
                      </Table.Cell>
                      <Table.Cell className={access.tone}>
                        <span title={intl.formatMessage({ id: "web.routes.audit_log.http_status", defaultMessage: "HTTP {status}", description: "Audit log: tooltip with the HTTP status code; {status} is the code" }, { status: e.status })}>{access.label}</span>
                      </Table.Cell>
                      <Table.Cell className="text-muted">{e.ip || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</Table.Cell>
                    </Table.Row>
                  );
                })}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table.Root>

        <div className="flex items-center justify-between border-t border-border px-5 py-3">
          <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.audit_log.page_size_pagesize_older_events_load_on", defaultMessage: "Page size {pageSize}. Older events load on demand.", description: "Audit log: sentence with pageSize in div" }, { pageSize: PAGE_SIZE })}</div>
          <Button
            variant="outline"
            size="sm"
            onPress={() => void query.fetchNextPage()}
            isDisabled={!query.hasNextPage || query.isFetchingNextPage}
          >
            {query.isFetchingNextPage
              ? intl.formatMessage({ id: "web.routes.audit_log.loading", defaultMessage: "Loading…", description: "Audit log: label of the load-more button while the next page loads" })
              : query.hasNextPage
                ? intl.formatMessage({ id: "web.routes.audit_log.load_more", defaultMessage: "Load more", description: "Audit log: button that loads the next page of events" })
                : intl.formatMessage({ id: "web.routes.audit_log.end_of_log", defaultMessage: "End of log", description: "Audit log: label shown when every event has been loaded" })}
          </Button>
        </div>
      </Card>
    </div>
  );
}

function renderIntegrityBanner(query: UseQueryResult<AuditVerifyResult>, intl: IntlShape): ReactNode {
  const data = query.data;
  if (query.isLoading) {
    return null;
  }
  if (query.isError) {
    return (
      <div className="flex items-center justify-between rounded-md border border-border bg-surface/50 px-4 py-3">
        <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.audit_log.integrity_status_unavailable", defaultMessage: "Integrity status unavailable", description: "Audit log: banner shown when the integrity check could not run" })}</div>
      </div>
    );
  }
  if (!data) {
    return null;
  }
  if (data.ok) {
    return (
      <div className="flex items-center justify-between rounded-md border border-success bg-success/5 px-4 py-3">
        <div className="flex items-center gap-2 text-sm font-medium text-success">
          <ShieldCheck className="h-4 w-4" />
          {intl.formatMessage({ id: "web.routes.audit_log.chain_verified", defaultMessage: "Audit chain verified — no tampering detected", description: "Audit log: banner shown when the audit hash chain verifies" })}
        </div>
        <Button
          size="sm"
          variant="outline"
          onPress={() => void query.refetch()}
          isDisabled={query.isFetching}
        >
          <RefreshCw className="h-4 w-4" />
          {intl.formatMessage({ id: "web.routes.audit_log.re_check", defaultMessage: "Re-check", description: "Audit log: button that re-runs the integrity check" })}
        </Button>
      </div>
    );
  }
  return (
    <AuditIntegrityBanner
      message={data.message || intl.formatMessage({ id: "web.routes.audit_log.integrity_check_failed", defaultMessage: "Integrity check failed — chain breaks at event #{id}", description: "Audit log: banner text when the chain breaks; {id} is the first event id that failed verification" }, { id: data.firstBadId })}
      onRefetch={() => void query.refetch()}
      isRefetching={query.isFetching}
    />
  );
}

// auditAction renders an audit row's method+path as a human-readable action
// ("Started server alpha") instead of raw HTTP, falling back to a generic
// "<verb> <resource>" when the route isn't specially known.
// Message descriptors for audit action labels. Raw values (an unknown HTTP
// method, an unknown lifecycle verb, a path segment) are shown as received.
const VERB_MSG: Record<string, MessageDescriptor> = defineMessages({
  POST: { id: "audit.verb.created", defaultMessage: "Created", description: "Audit log: action verb for a POST request" },
  PUT: { id: "audit.verb.updated", defaultMessage: "Updated", description: "Audit log: action verb for a PUT or PATCH request" },
  PATCH: { id: "audit.verb.updated", defaultMessage: "Updated", description: "Audit log: action verb for a PUT or PATCH request" },
  DELETE: { id: "audit.verb.deleted", defaultMessage: "Deleted", description: "Audit log: action verb for a DELETE request" },
  GET: { id: "audit.verb.viewed", defaultMessage: "Viewed", description: "Audit log: action verb for a GET request" },
});
const LIFECYCLE_MSG: Record<string, MessageDescriptor> = defineMessages({
  start: { id: "audit.verb.started", defaultMessage: "Started", description: "Audit log: action verb for starting a game server" },
  stop: { id: "audit.verb.stopped", defaultMessage: "Stopped", description: "Audit log: action verb for stopping a game server" },
  restart: { id: "audit.verb.restarted", defaultMessage: "Restarted", description: "Audit log: action verb for restarting a game server" },
  clone: { id: "audit.verb.cloned", defaultMessage: "Cloned", description: "Audit log: action verb for cloning a game server" },
  "wipe-data": { id: "audit.verb.wiped_data_on", defaultMessage: "Wiped data on", description: "Audit log: action verb for wiping a game server's world data" },
  transfer: { id: "audit.verb.transferred_ownership_of", defaultMessage: "Transferred ownership of", description: "Audit log: action verb for transferring a game server's ownership" },
});
const NOUN_MSG: Record<string, MessageDescriptor> = defineMessages({
  server: { id: "audit.noun.server", defaultMessage: "server", description: "Audit log: object type in an action label, a game server" },
  backup: { id: "audit.noun.backup", defaultMessage: "backup", description: "Audit log: object type in an action label, a backup" },
  backup_schedule: { id: "audit.noun.backup_schedule", defaultMessage: "backup schedule", description: "Audit log: object type in an action label, a backup schedule" },
  backup_destination: { id: "audit.noun.backup_destination", defaultMessage: "backup destination", description: "Audit log: object type in an action label, a backup destination" },
  restore: { id: "audit.noun.restore", defaultMessage: "restore", description: "Audit log: object type in an action label, a restore" },
  user: { id: "audit.noun.user", defaultMessage: "user", description: "Audit log: object type in an action label, a user account" },
  role: { id: "audit.noun.role", defaultMessage: "role", description: "Audit log: object type in an action label, a role" },
  module_source: { id: "audit.noun.module_source", defaultMessage: "module source", description: "Audit log: object type in an action label, a module source" },
  module: { id: "audit.noun.module", defaultMessage: "module", description: "Audit log: object type in an action label, a module" },
  cluster: { id: "audit.noun.cluster", defaultMessage: "cluster", description: "Audit log: object type in an action label, a cluster" },
});
const AUDIT_ACTION_MSG: Record<string, MessageDescriptor> = defineMessages({
  object: { id: "audit.action.object", defaultMessage: "{action} {noun}{target}", description: "Audit log: action label such as 'Created server alpha'; {action} is the verb, {noun} is the object type, {target} is empty or a space followed by the object name" },
  restored_backup: { id: "audit.action.restored_backup", defaultMessage: "Restored backup{target}", description: "Audit log: action label for a backup restore; {target} is empty or a space followed by the backup name" },
  settings_updated: { id: "audit.action.settings_updated", defaultMessage: "Updated settings", description: "Audit log: action label for a change to panel settings" },
  signed_in: { id: "audit.action.signed_in", defaultMessage: "Signed in", description: "Audit log: action label for a successful sign-in" },
  failed_sign_in: { id: "audit.action.failed_sign_in", defaultMessage: "Failed sign-in", description: "Audit log: action label for a sign-in attempt that was rejected" },
  fallback: { id: "audit.action.fallback", defaultMessage: "{action} {resource}", description: "Audit log: fallback action label for an unrecognised route; {action} is the verb, {resource} is the last path segment" },
});

// auditAction takes the English fallback when no intl is passed (tests and
// non-React callers), so its output is unchanged for them.
export function auditAction(
  e: { method: string; path: string; target?: string; status?: number },
  intl: IntlShape = englishIntl,
): string {
  // Strip a leading /api/v<n> prefix so route matching is version-agnostic.
  const p = e.path.replace(/^\/api\/v\d+/, "").replace(/\/+$/, "") || "/";
  const t = e.target ? ` ${e.target}` : "";
  const word = (map: Record<string, MessageDescriptor>, key: string): string => {
    const d = map[key];
    return d ? intl.formatMessage(d) : key;
  };
  const object = (action: string, noun: string): string =>
    intl.formatMessage(AUDIT_ACTION_MSG.object, { action, noun: word(NOUN_MSG, noun), target: t });
  // Lifecycle verbs are encoded as POST /servers/{name}:<verb>.
  const colon = /\/servers\/[^/]+:([a-z-]+)$/.exec(p);
  if (colon) return object(word(LIFECYCLE_MSG, colon[1]), "server");
  const verb = word(VERB_MSG, e.method);
  const m = (s: string) => p === s || p.startsWith(s + "/");
  if (m("/servers")) return object(verb, "server");
  if (m("/backups")) return object(verb, "backup");
  if (m("/restores")) return e.method === "POST"
    ? intl.formatMessage(AUDIT_ACTION_MSG.restored_backup, { target: t })
    : object(verb, "restore");
  if (m("/schedules")) return object(verb, "backup_schedule");
  if (m("/users")) return object(verb, "user");
  if (m("/roles")) return object(verb, "role");
  if (m("/modules/sources")) return object(verb, "module_source");
  if (m("/modules")) return object(verb, "module");
  if (m("/destinations") || m("/backup-destinations")) return object(verb, "backup_destination");
  if (m("/admin/config")) return intl.formatMessage(AUDIT_ACTION_MSG.settings_updated);
  if (m("/auth/login")) return intl.formatMessage(
    e.status !== undefined && e.status >= 400 ? AUDIT_ACTION_MSG.failed_sign_in : AUDIT_ACTION_MSG.signed_in,
  );
  if (m("/cluster")) return object(verb, "cluster");
  // Fallback: verb + last path segment.
  const seg = p.split("/").filter(Boolean).pop() ?? "resource";
  return intl.formatMessage(AUDIT_ACTION_MSG.fallback, { action: verb, resource: seg });
}

// accessOutcome maps an HTTP status to the allow/deny framing the audit log
// presents (403 = explicitly denied; other 4xx/5xx = failed; 2xx/3xx = ok).
function accessOutcome(status: number, intl: IntlShape): { label: string; tone: string } {
  if (status === 403) return { label: intl.formatMessage(ACCESS_MSG.denied), tone: "text-danger" };
  if (status >= 500) return { label: intl.formatMessage(ACCESS_MSG.error), tone: "text-danger" };
  if (status >= 400) return { label: intl.formatMessage(ACCESS_MSG.failed), tone: "text-warning" };
  return { label: intl.formatMessage(ACCESS_MSG.allowed), tone: "text-success" };
}

const ACCESS_MSG = defineMessages({
  denied: { id: "audit.access.denied", defaultMessage: "Denied", description: "Audit log: access column for a 403 response" },
  error: { id: "audit.access.error", defaultMessage: "Error", description: "Audit log: access column for a 5xx response" },
  failed: { id: "audit.access.failed", defaultMessage: "Failed", description: "Audit log: access column for a non-403 4xx response" },
  allowed: { id: "audit.access.allowed", defaultMessage: "Allowed", description: "Audit log: access column for a 2xx or 3xx response" },
});

function MethodPill({ method }: { method: string }) {
  const variant = methodVariant(method);
  return (
    <Chip
      size="sm"
      variant="soft"
      className="text-[10px] font-semibold uppercase"
      color={variant}
    >
      {method}
    </Chip>
  );
}

function methodVariant(m: string): "default" | "success" | "warning" | "danger" {
  switch (m) {
    case "GET":    return "default";
    case "POST":   return "success";
    case "PUT":
    case "PATCH":  return "warning";
    case "DELETE": return "danger";
    default:       return "default";
  }
}

function statusBucket(s: number): "2xx" | "4xx" | "5xx" | "other" {
  if (s >= 200 && s < 300) return "2xx";
  if (s >= 400 && s < 500) return "4xx";
  if (s >= 500 && s < 600) return "5xx";
  return "other";
}

function labelFor(s: StatusClass, intl: IntlShape): string {
  if (s === "all") return intl.formatMessage({ id: "web.routes.audit_log.status_all", defaultMessage: "All", description: "Audit log: status filter tab that shows every event" });
  return s;
}
