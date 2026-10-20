import { ListBox, ListBoxItem, Select } from "@heroui/react";
import type { FleetIssue } from "@/lib/fleet";
import { useOptionalIntl } from "@/lib/i18n/format";

export function FleetScopeFilter({ value, onChange, clusters }: { value: string; onChange: (value: string) => void; clusters: string[] }) {
  const intl = useOptionalIntl();
  const choices = [...new Set([...clusters, ...(value ? [value] : [])].filter(Boolean))].sort();
  return <Select value={value} onChange={(next) => onChange(String(next ?? ""))} aria-label={intl.formatMessage({ id: "web.components.fleet_scope.filter_by_location", defaultMessage: "Filter by location", description: "Fleet scope: screen-reader label for the location dropdown that narrows the view to one location; not visible on screen" })} className="w-full max-w-xs">
    <Select.Trigger><Select.Value /><Select.Indicator className="ml-auto h-4 w-4" /></Select.Trigger>
    <Select.Popover><ListBox aria-label={intl.formatMessage({ id: "web.components.fleet_scope.location_options", defaultMessage: "Location options", description: "Fleet scope: screen-reader name of the list of location choices inside the location dropdown; not visible on screen" })}>
      <ListBoxItem id="" textValue={intl.formatMessage({ id: "web.components.fleet_scope.all_locations_text", defaultMessage: "All locations", description: "Fleet scope: typeahead text of the all-locations option" })}>{intl.formatMessage({ id: "web.components.fleet_scope.all_locations", defaultMessage: "All locations", description: "Fleet scope: first option in the location dropdown that clears the location filter and shows every location; keep it short" })}</ListBoxItem>
      {choices.map((cluster) => <ListBoxItem key={cluster} id={cluster} textValue={cluster}>{cluster}</ListBoxItem>)}
    </ListBox></Select.Popover>
  </Select>;
}

export function FleetCoverage({ partial, issues = [], error, label: labelProp }: { partial?: boolean; issues?: FleetIssue[]; error?: unknown; label?: string }) {
  const intl = useOptionalIntl();
  const label = labelProp ?? intl.formatMessage({ id: "web.components.fleet_scope.results", defaultMessage: "Results", description: "Fleet scope: default noun for the returned resources in the coverage notice, used when no label is passed" });
  if (error) return <div role="alert" className="rounded-lg border border-danger/40 bg-danger/5 p-3 text-sm">{intl.formatMessage({ id: "web.components.fleet_scope.label_are_unavailable_try_again", defaultMessage: "{label} are unavailable. Try again.", description: "Fleet scope: sentence with label in div" }, { label: label })}</div>;
  if (!partial) return null;
  return <div role="status" className="rounded-lg border border-warning/40 bg-warning/5 p-3 text-sm">
    <strong>{intl.formatMessage({ id: "web.components.fleet_scope.label_are_partial", defaultMessage: "{label} are partial.", description: "Fleet scope: sentence with label in strong" }, { label: label })}</strong> {intl.formatMessage({ id: "web.components.fleet_scope.counts_cover_only_returned_resources", defaultMessage: "Counts cover only returned resources.", description: "Fleet scope: second part of the partial-results warning, after the heading, saying counts include only the resources that were returned" })}{issues.length > 0 && <ul className="mt-1 list-inside list-disc">{issues.map((issue, index) => <li key={`${issue.cluster}/${issue.namespace ?? ""}/${issue.code}/${index}`}>
      {intl.formatMessage({ id: "web.components.fleet_scope.scope_message", defaultMessage: "{scope}: {message}", description: "Fleet scope: warning line; {scope} is the cluster and namespace or the selected scope label, {message} is the warning text" }, { scope: [issue.cluster, issue.namespace].filter(Boolean).join(" / ") || intl.formatMessage({ id: "web.components.fleet_scope.selected_scope", defaultMessage: "Selected scope", description: "Fleet scope: label of the selected scope when no cluster or namespace is set" }), message: issue.message || (issue.code === "limit" ? intl.formatMessage({ id: "web.components.fleet_scope.limit_reached", defaultMessage: "Result limit reached.", description: "Fleet scope: warning shown when results are capped" }) : intl.formatMessage({ id: "web.components.fleet_scope.unavailable", defaultMessage: "Unavailable.", description: "Fleet scope: warning shown when a cluster cannot be queried" })) })}
    </li>)}</ul>}
  </div>;
}
