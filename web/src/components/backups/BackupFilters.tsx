import type { ResourceTarget } from "@/lib/resourceTarget";
import { useState, type ReactNode } from "react";
import { Chip, Input, Select, ListBox, ListBoxItem } from "@heroui/react";
import { Filter } from "lucide-react";
import { FleetScopeFilter } from "@/components/FleetScope";
import { FilterPopover } from "@/components/ui/FilterPopover";
import type { GameServer } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape } from "react-intl";

// Display labels for backup and restore phase values. The phase value keeps
// driving the filter; only the option text is translated. Unknown values show raw.
const phaseMessages = defineMessages({
  Pending: { id: "web.components.backups.backup_filters.phase_pending", defaultMessage: "Pending", description: "Backup filters: option label for the Pending backup or restore phase" },
  Running: { id: "web.components.backups.backup_filters.phase_running", defaultMessage: "Running", description: "Backup filters: option label for the Running backup or restore phase" },
  Succeeded: { id: "web.components.backups.backup_filters.phase_succeeded", defaultMessage: "Succeeded", description: "Backup filters: option label for the Succeeded backup or restore phase" },
  Failed: { id: "web.components.backups.backup_filters.phase_failed", defaultMessage: "Failed", description: "Backup filters: option label for the Failed backup or restore phase" },
  Suspending: { id: "web.components.backups.backup_filters.phase_suspending", defaultMessage: "Suspending", description: "Backup filters: option label for the Suspending restore phase" },
  Resuming: { id: "web.components.backups.backup_filters.phase_resuming", defaultMessage: "Resuming", description: "Backup filters: option label for the Resuming restore phase" },
});

function phaseLabel(intl: IntlShape, value: string): string {
  return Object.prototype.hasOwnProperty.call(phaseMessages, value)
    ? intl.formatMessage(phaseMessages[value as keyof typeof phaseMessages])
    : value;
}

interface Props {
  search: string;
  onSearchChange: (v: string) => void;
  location: string;
  onLocationChange: (v: string) => void;
  locations: string[];
  server: string;
  onServerChange: (v: string) => void;
  phase?: string;
  onPhaseChange?: (v: string) => void;
  servers: (GameServer & { fleetTarget?: ResourceTarget })[];
  phases?: string[];
  trailing?: ReactNode;
}

export function BackupFilters({
  search, onSearchChange,
  location, onLocationChange, locations,
  server, onServerChange,
  phase = "", onPhaseChange,
  servers, phases = [], trailing,
}: Props) {
  const intl = useOptionalIntl();
  const [isOpen, setIsOpen] = useState(false);
  const [draftLocation, setDraftLocation] = useState(location);
  const [draftServer, setDraftServer] = useState(server);
  const [draftPhase, setDraftPhase] = useState(phase);
  const count = Number(Boolean(location)) + Number(Boolean(server)) + Number(Boolean(phase));
  const availableServers = servers.filter((item) => !draftLocation || item.fleetTarget?.cluster === draftLocation);
  const onOpenChange = (open: boolean) => {
    if (open) {
      setDraftLocation(location);
      setDraftServer(server);
      setDraftPhase(phase);
    }
    setIsOpen(open);
  };
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <Input
        className="min-w-0 flex-1 basis-40 max-w-xs"
        placeholder={intl.formatMessage({ id: "web.components.backups.backup_filters.search_by_name_or_server", defaultMessage: "Search by name or server…", description: "Backup filters: placeholder text inside the search box giving an example of what to type, a backup name or server name" })}
        aria-label={intl.formatMessage({ id: "web.components.backups.backup_filters.search_backups_schedules_or_restores", defaultMessage: "Search backups, schedules or restores", description: "Backup filters: screen-reader label for the search box that finds backups, schedules or restores; not visible on screen" })}
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        type="text"
      />
      <div className="ml-auto flex items-center gap-3">
        {trailing && <div className="text-xs text-muted">{trailing}</div>}
        <FilterPopover
          isOpen={isOpen}
          onOpenChange={onOpenChange}
          onApply={() => {
            onLocationChange(draftLocation);
            onServerChange(draftServer);
            onPhaseChange?.(draftPhase);
            setIsOpen(false);
          }}
          onClear={() => {
            setDraftLocation("");
            setDraftServer("");
            setDraftPhase("");
          }}
          fields={<>
            <div className="space-y-1">
              <div className="text-xs font-semibold text-muted">{intl.formatMessage({ id: "web.components.backups.backup_filters.location", defaultMessage: "Location", description: "Backup filters: label above the location dropdown in the filter panel" })}</div>
              <FleetScopeFilter value={draftLocation} onChange={(next) => {
                setDraftLocation(next);
                if (next && servers.find((item) => serverOptionID(item) === draftServer)?.fleetTarget?.cluster !== next) setDraftServer("");
              }} clusters={locations} />
            </div>
            <div className="space-y-1">
              <div className="text-xs font-semibold text-muted">{intl.formatMessage({ id: "web.components.backups.backup_filters.server", defaultMessage: "Server", description: "Backup filters: label above the server dropdown in the filter panel" })}</div>
              <Select value={draftServer} onChange={(v) => setDraftServer(String(v ?? ""))} className="w-full" aria-label={intl.formatMessage({ id: "web.components.backups.backup_filters.filter_by_server", defaultMessage: "Filter by server", description: "Backup filters: screen-reader label for the server dropdown in the filter panel; not visible on screen" })}>
                <Select.Trigger><Select.Value /><Select.Indicator className="ml-auto h-4 w-4" /></Select.Trigger>
                <Select.Popover>
                  <ListBox aria-label={intl.formatMessage({ id: "web.components.backups.backup_filters.server_options", defaultMessage: "Server options", description: "Backup filters: screen-reader name of the list of server choices in the server dropdown; not visible on screen" })}>
                    <ListBoxItem id="" textValue={intl.formatMessage({ id: "web.components.backups.backup_filters.all_servers_text", defaultMessage: "All servers", description: "Backup filters: typeahead text of the all-servers option" })}>{intl.formatMessage({ id: "web.components.backups.backup_filters.all_servers", defaultMessage: "All servers", description: "Backup filters: first server dropdown option that clears the server filter so backups from all servers show; keep it short" })}</ListBoxItem>
                    {availableServers.map((s) => (
                      <ListBoxItem key={serverOptionID(s)} id={serverOptionID(s)} textValue={serverOptionLabel(s, intl)}>
                        {serverOptionLabel(s, intl)}
                      </ListBoxItem>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>
            {phases.length > 0 && <div className="space-y-1">
              <div className="text-xs font-semibold text-muted">{intl.formatMessage({ id: "web.components.backups.backup_filters.phase", defaultMessage: "Phase", description: "Backup filters: label above the phase dropdown in the filter panel; the dropdown appears only when phases exist" })}</div>
              <Select value={draftPhase} onChange={(v) => setDraftPhase(String(v ?? ""))} className="w-full" aria-label={intl.formatMessage({ id: "web.components.backups.backup_filters.filter_by_phase", defaultMessage: "Filter by phase", description: "Backup filters: screen-reader label for the phase dropdown in the filter panel; not visible on screen" })}>
                <Select.Trigger><Select.Value /><Select.Indicator className="ml-auto h-4 w-4" /></Select.Trigger>
                <Select.Popover>
                  <ListBox aria-label={intl.formatMessage({ id: "web.components.backups.backup_filters.phase_options", defaultMessage: "Phase options", description: "Backup filters: screen-reader name of the list of phase choices in the phase dropdown; not visible on screen" })}>
                    <ListBoxItem id="" textValue={intl.formatMessage({ id: "web.components.backups.backup_filters.all_phases_text", defaultMessage: "All phases", description: "Backup filters: typeahead text of the all-phases option" })}>{intl.formatMessage({ id: "web.components.backups.backup_filters.all_phases", defaultMessage: "All phases", description: "Backup filters: first phase dropdown option that clears the phase filter so backups in every phase show; keep it short" })}</ListBoxItem>
                    {phases.map((p) => <ListBoxItem key={p} id={p} textValue={phaseLabel(intl, p)}>{phaseLabel(intl, p)}</ListBoxItem>)}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>}
          </>}
        >
          <div className="inline-flex items-center gap-2 rounded-[6px] px-3 py-2 text-sm font-medium border border-default-300 bg-default-100 hover:bg-default-200 cursor-pointer transition-colors">
            <Filter className="h-4 w-4" /> {intl.formatMessage({ id: "web.components.backups.backup_filters.filter", defaultMessage: "Filter", description: "Backup filters: label of the Filter button that opens the filter panel, with a count chip showing how many filters are set; keep it short" })}{count > 0 && <Chip size="sm" variant="soft">{count}</Chip>}
          </div>
        </FilterPopover>
      </div>
    </div>
  );
}

function serverOptionID(s: GameServer & { fleetTarget?: ResourceTarget }) {
  const t = s.fleetTarget;
  return t ? JSON.stringify([t.cluster, t.namespace, t.name, t.uid]) : s.metadata.name;
}
function serverOptionLabel(s: GameServer & { fleetTarget?: ResourceTarget }, intl: IntlShape) {
  const t = s.fleetTarget;
  return t ? intl.formatMessage({ id: "web.components.backups.backup_filters.server_option_label", defaultMessage: "{name} · {cluster} / {namespace}", description: "Backup filters: server option label with its location; {name} is the server name, {cluster} the cluster, {namespace} the namespace" }, { name: s.metadata.name, cluster: t.cluster, namespace: t.namespace }) : s.metadata.name;
}
