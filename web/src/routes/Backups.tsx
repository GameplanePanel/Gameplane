import { useMemo, useState } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { createResourceClient } from "@/lib/endpoints";
import { useFleetLocation, Fleet, located, targetKey, targetLabel, type Located } from "@/lib/fleet";
import { FleetCoverage } from "@/components/FleetScope";
import {
  Button,
  Card,
  Select,
  ListBox,
  ListBoxItem,
  Tabs,
  Tab,
  Table,
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  Switch,
} from "@heroui/react";
import { PageHeader } from "@/components/PageHeader";
import { formatRelative, formatRelativeFuture } from "@/lib/utils";
import { PhaseChip } from "@/components/ui/PhaseChip";
import { ErrorBanner } from "@/components/backups/ErrorBanner";
import { ScheduleForm } from "@/components/backups/ScheduleForm";
import { RestoreDialog } from "@/components/backups/RestoreDialog";
import { BackupDetailDrawer } from "@/components/backups/BackupDetailDrawer";
import { BackupRow } from "@/components/backups/BackupRow";
import { BackupFilters } from "@/components/backups/BackupFilters";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import type { Backup, BackupSchedule } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

type TabKey = "backups" | "schedules" | "restores";
const TAB_LABELS = defineMessages({
  backups: { id: "web.routes.backups.tab_backups", defaultMessage: "Backups", description: "Backups: label of the backups tab" },
  schedules: { id: "web.routes.backups.tab_schedules", defaultMessage: "Schedules", description: "Backups: label of the schedules tab" },
  restores: { id: "web.routes.backups.tab_restores", defaultMessage: "Restores", description: "Backups: label of the restores tab" },
});
const TABS: { id: TabKey; label: MessageDescriptor }[] = [
  { id: "backups", label: TAB_LABELS.backups },
  { id: "schedules", label: TAB_LABELS.schedules },
  { id: "restores", label: TAB_LABELS.restores },
];

// Option label in the server pickers: "<server name> · <cluster> / <namespace>".
const SERVER_OPTION_LABEL: Record<string, MessageDescriptor> = defineMessages({
  label: { id: "web.routes.backups.server_option_label", defaultMessage: "{name} · {target}", description: "Backups: server picker option; {name} is the server name, {target} is its cluster and namespace" },
});

// Count shown after the filters, e.g. "1 of 2 backups".
const TRAILING_COUNT: Record<"backups" | "schedules" | "restores", MessageDescriptor> = defineMessages({
  backups: { id: "web.routes.backups.trailing_backups", defaultMessage: "{shown} of {total} {total, plural, one {backup} other {backups}}", description: "Backups: count shown after the filters on the backups tab; {shown} is the number listed, {total} the number loaded" },
  schedules: { id: "web.routes.backups.trailing_schedules", defaultMessage: "{shown} of {total} {total, plural, one {schedule} other {schedules}}", description: "Backups: count shown after the filters on the schedules tab; {shown} is the number listed, {total} the number loaded" },
  restores: { id: "web.routes.backups.trailing_restores", defaultMessage: "{shown} of {total} {total, plural, one {restore} other {restores}}", description: "Backups: count shown after the filters on the restores tab; {shown} is the number listed, {total} the number loaded" },
});

const BACKUP_PHASES = ["Pending", "Running", "Succeeded", "Failed"];
const RESTORE_PHASES = ["Pending", "Suspending", "Running", "Resuming", "Succeeded", "Failed"];

export function BackupsPage() {
  const intl = useOptionalIntl();
  const search = useLocation().search;
  const navigate = useNavigate();
  const tab: TabKey = search.tab === "schedules" || search.tab === "restores" ? search.tab : "backups";
  const setTab = (next: TabKey) => {
    void navigate({ to: "/backups", replace: true, search: (previous) => ({ ...previous, tab: next === "backups" ? undefined : next }) });
  };
  const [backupNow, setBackupNow] = useState(false);
  const [location, setLocation] = useFleetLocation();
  const { data: serverScopes } = useQuery({ queryKey: ["fleet", "servers", "", ""], queryFn: ({ signal }) => Fleet.servers({}, signal) });
  const backupScopes = useQueries({ queries: (["backups", "schedules", "restores"] as const).map((kind) => ({
    queryKey: ["fleet", kind, ""], queryFn: ({ signal }: { signal: AbortSignal }) => Fleet[kind]({}, signal),
  })) });
  const locations = [...(serverScopes?.scopes ?? []).map((scope) => scope.cluster), ...(serverScopes?.items ?? []).map((item) => item.target.cluster), ...backupScopes.flatMap((query) => [...(query.data?.scopes ?? []).map((scope) => scope.cluster), ...(query.data?.items ?? []).map((item) => item.target.cluster)])];

  return (
    <div className="space-y-5 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.backups.backups", defaultMessage: "Backups", description: "Backups: page title at the top of the backups screen, also the name of the backups tab and its table; keep it short" })}
        subtitle={intl.formatMessage({ id: "web.routes.backups.snapshots_schedules_and_restores_across", defaultMessage: "Snapshots, schedules, and restores across your authorized locations.", description: "Backups: subtitle under the page title saying the screen covers snapshots, schedules and restores across your authorised locations" })}
        actions={
          <Button onPress={() => setBackupNow(true)}>
            <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.backups.back_up_now", defaultMessage: "Back up now", description: "Backups: button that opens the dialog for a one-off snapshot, and the heading of that dialog; keep it short" })}</Button>
        }
      />
      {backupNow && <BackupNowDialog onClose={() => setBackupNow(false)} />}
      <Tabs selectedKey={tab} onSelectionChange={(key) => setTab(key as TabKey)}>
        <Tabs.List>
          {TABS.map((t) => (
            <Tab key={t.id} id={t.id}>
              {intl.formatMessage(t.label)}
            </Tab>
          ))}
        </Tabs.List>
      </Tabs>
      {tab === "backups" && <BackupsTabPanel location={location} onLocationChange={setLocation} locations={locations} />}
      {tab === "schedules" && <SchedulesTabPanel location={location} onLocationChange={setLocation} locations={locations} />}
      {tab === "restores" && <RestoresTabPanel location={location} onLocationChange={setLocation} locations={locations} />}
    </div>
  );
}

type LocationFilterProps = { location: string; onLocationChange: (location: string) => void; locations: string[] };

function BackupsTabPanel({ location, onLocationChange, locations }: LocationFilterProps) {
  const intl = useOptionalIntl();
  const [search, setSearch] = useState("");
  const [server, setServer] = useState("");
  const [phase, setPhase] = useState("");
  const [restoringBackup, setRestoringBackup] = useState<Located<Backup> | null>(null);
  const [selectedBackup, setSelectedBackup] = useState<Located<Backup> | null>(null);

  const { data: backupsFleet, error: fleetError, isLoading: fleetLoading } = useQuery({
    queryKey: ["fleet", "backups", location],
    queryFn: ({ signal }) => Fleet.backups({ cluster: location || undefined }, signal),
    refetchInterval: 5000,
  });
  const backups = useMemo(() => ({ items: (backupsFleet?.items ?? []).map(located) }), [backupsFleet]);
  const { data: serversFleet } = useQuery({
    queryKey: ["fleet", "servers", "", ""],
    queryFn: ({ signal }) => Fleet.servers({}, signal),
  });
  const serversList = { items: (serversFleet?.items ?? []).map(located) };

  const items = useMemo(() => backups?.items ?? [], [backups]);
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return items.filter((b) => {
      if (server && !matchesServer(b, server, serversList.items)) return false;
      if (phase && b.status?.phase !== phase) return false;
      if (q) {
        const hay = `${b.metadata.name} ${b.spec.serverRef.name}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [items, search, server, phase, serversList.items]);

  return (
    <div className="space-y-4">
      <FleetCoverage partial={backupsFleet?.partial} issues={backupsFleet?.issues} error={fleetError} label={intl.formatMessage({ id: "web.routes.backups.backups", defaultMessage: "Backups", description: "Backups: page title at the top of the backups screen, also the name of the backups tab and its table; keep it short" })} />
      {fleetLoading && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.backups.loading_backups", defaultMessage: "Loading backups…", description: "Backups: loading text on the backups tab while backup data is fetched" })}</p>}
      <BackupFilters
        location={location}
        onLocationChange={onLocationChange}
        locations={locations}
        search={search}
        onSearchChange={setSearch}
        server={server}
        onServerChange={setServer}
        phase={phase}
        onPhaseChange={setPhase}
        servers={serversList?.items ?? []}
        phases={BACKUP_PHASES}
        trailing={intl.formatMessage(TRAILING_COUNT.backups, { shown: filtered.length, total: items.length })}
      />

      <div className="rounded-lg border border-border bg-card overflow-hidden">
        <Table.Root>
          <Table.ScrollContainer>
            <Table.Content aria-label={intl.formatMessage({ id: "web.routes.backups.backups", defaultMessage: "Backups", description: "Backups: page title at the top of the backups screen, also the name of the backups tab and its table; keep it short" })} keyboardNavigationBehavior="arrow">
              <Table.Header>
                <Table.Column key="name" isRowHeader>{intl.formatMessage({ id: "web.routes.backups.name", defaultMessage: "Name", description: "Backups: column heading for the name of each backup, schedule or restore; keep it short" })}</Table.Column>
                <Table.Column key="server">{intl.formatMessage({ id: "web.routes.backups.server", defaultMessage: "Server", description: "Backups: column heading for the server a row belongs to, and the accessible name of the server dropdown in the backup dialog; keep it short" })}</Table.Column>
                <Table.Column key="phase">{intl.formatMessage({ id: "web.routes.backups.phase", defaultMessage: "Phase", description: "Backups: column heading for the phase or state of a backup or restore; keep it short" })}</Table.Column>
                <Table.Column key="size">{intl.formatMessage({ id: "web.routes.backups.size", defaultMessage: "Size", description: "Backups: column heading for the size of each backup; keep it short" })}</Table.Column>
                <Table.Column key="completed">{intl.formatMessage({ id: "web.routes.backups.completed", defaultMessage: "Completed", description: "Backups: column heading for when a backup or restore completed; keep it short" })}</Table.Column>
                <Table.Column key="actions" className="text-end" />
              </Table.Header>
              <Table.Body
                renderEmptyState={() => (
                  <>
                    {items.length === 0
                      ? (backupsFleet?.partial || fleetError
                        ? intl.formatMessage({ id: "web.routes.backups.no_backup_data_available", defaultMessage: "No backup data available.", description: "Backups: empty state when the backup list could not be loaded" })
                        : intl.formatMessage({ id: "web.routes.backups.no_backups_yet", defaultMessage: "No backups yet. Use \"Back up now\" to create the first one.", description: "Backups: empty state when no backups exist; quoted text is the Back up now button label" }))
                      : intl.formatMessage({ id: "web.routes.backups.no_backups_match_the_current_filters", defaultMessage: "No backups match the current filters.", description: "Backups: empty state when filters hide every backup" })}
                  </>
                )}
              >
                {filtered.map((b) => (
                  <BackupRow
                    key={targetKey(b.fleetTarget)}
                    target={b.fleetTarget}
                    permissions={b.fleetPermissions}
                    backup={b}
                    showServer
                    onSelect={() => setSelectedBackup(b)}
                    onRestore={() => setRestoringBackup(b)}
                  />
                ))}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table.Root>
      </div>

      <RestoreDialog
        backup={restoringBackup}
        target={restoringBackup?.fleetTarget}
        permissions={restoringBackup?.fleetPermissions}
        onClose={() => setRestoringBackup(null)}
      />
      <BackupDetailDrawer
        name={selectedBackup?.metadata.name ?? null}
        target={selectedBackup?.fleetTarget}
        permissions={selectedBackup?.fleetPermissions}
        onClose={() => setSelectedBackup(null)}
        onRestore={(b) => {
          if (selectedBackup) setRestoringBackup({ ...b, fleetTarget: selectedBackup.fleetTarget, fleetPermissions: selectedBackup.fleetPermissions });
          setSelectedBackup(null);
        }}
      />
    </div>
  );
}

// The "Back up now" form, relocated from an inline card above the table to a
// dialog launched from the page header. The query and mutation logic is unchanged;
// the dialog closes once the snapshot starts.
function BackupNowDialog({ onClose }: { onClose: () => void }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const [createServer, setCreateServer] = useState("");
  const [createDest, setCreateDest] = useState("");

  const { data: serversFleet } = useQuery({
    queryKey: ["fleet", "servers", "", ""],
    queryFn: ({ signal }) => Fleet.servers({}, signal),
  });
  const serversList = { items: (serversFleet?.items ?? []).map(located) };
  const selectedServer = serversList.items.find((item) => targetKey(item.fleetTarget) === createServer);
  const { data: destinationData } = useQuery({
    queryKey: ["backup-destinations", selectedServer?.fleetTarget.cluster, selectedServer?.fleetTarget.namespace],
    queryFn: () => createResourceClient(selectedServer!.fleetTarget).BackupDestinations.list(),
    enabled: !!selectedServer,
  });
  const destinations = destinationData?.items ?? [];

  // When destinations resolve, default to the first one. The user can
  // still pick a different one if more than one exists. Adjusted directly
  // during render (not in an effect) — the condition becomes false as soon
  // as createDest is set, so this can't loop.
  if (!createDest && destinations.length > 0) {
    setCreateDest(destinations[0].name);
  }

  const createNow = useMutation({
    mutationFn: () => {
      if (!selectedServer || !hasPermission(selectedServer.fleetPermissions, "backups:write")) throw new Error(intl.formatMessage({ id: "web.routes.backups.select_server_with_backup_access", defaultMessage: "Select a server with backup access.", description: "Backups: error shown when the chosen server has no backup permission" }));
      return createResourceClient(selectedServer.fleetTarget).Backups.create({
        serverRef: { name: selectedServer.metadata.name },
        repoRef: { name: createDest, key: "repo" },
      }, selectedServer.fleetTarget.namespace);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet", "backups"] });
      onClose();
    },
  });

  const noDestinations = destinations.length === 0;

  return (
    <Modal isOpen onOpenChange={(open) => { if (!open) onClose(); }}>
      <ModalBackdrop isDismissable={!createNow.isPending} isKeyboardDismissDisabled={createNow.isPending}>
        <ModalContainer>
          <ModalDialog>
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.routes.backups.back_up_now", defaultMessage: "Back up now", description: "Backups: button that opens the dialog for a one-off snapshot, and the heading of that dialog; keep it short" })}</ModalHeading>
            </ModalHeader>
            <ModalBody className="gap-4">
              <p className="text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.backups.run_a_one_off_snapshot_outside_any", defaultMessage: "Run a one-off snapshot outside any schedule.", description: "Backups: text in the back-up-now dialog explaining that it runs a one-off snapshot outside any schedule" })}</p>
              <div className="space-y-4">
                <div>
                  <Select
                    value={createServer}
                    onChange={(v) => { setCreateServer(v as string); setCreateDest(""); }}
                    placeholder={intl.formatMessage({ id: "web.routes.backups.select_a_server", defaultMessage: "Select a server…", description: "Backups: grey placeholder of the server dropdown, asking the user to pick a server in the backup dialog or the new schedule form" })}
                    aria-label={intl.formatMessage({ id: "web.routes.backups.server", defaultMessage: "Server", description: "Backups: column heading for the server a row belongs to, and the accessible name of the server dropdown in the backup dialog; keep it short" })}
                    className="mt-1"
                  >
                    <Select.Trigger>
                      <Select.Value />
                      <Select.Indicator className="ml-auto h-4 w-4" />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox aria-label={intl.formatMessage({ id: "web.routes.backups.server_options", defaultMessage: "Server options", description: "Backups: accessible name of the list of servers shown when the server dropdown is open" })}>
                        {(serversList?.items ?? []).filter((item) => hasPermission(item.fleetPermissions, "backups:write")).map((s) => (
                          <ListBoxItem key={targetKey(s.fleetTarget)} id={targetKey(s.fleetTarget)} textValue={intl.formatMessage(SERVER_OPTION_LABEL.label, { name: s.metadata.name, target: targetLabel(s.fleetTarget) })}>
                            {intl.formatMessage(SERVER_OPTION_LABEL.label, { name: s.metadata.name, target: targetLabel(s.fleetTarget) })}
                          </ListBoxItem>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>
                {destinations.length > 1 && (
                  <div>
                    <Select
                      value={createDest}
                      onChange={(v) => setCreateDest(v as string)}
                      placeholder={intl.formatMessage({ id: "web.routes.backups.select_a_destination", defaultMessage: "Select a destination…", description: "Backups: grey placeholder of the destination dropdown in the back-up-now dialog, asking the user to pick a backup destination" })}
                      aria-label={intl.formatMessage({ id: "web.routes.backups.destination", defaultMessage: "Destination", description: "Backups: accessible name of the backup destination dropdown in the back-up-now dialog" })}
                      className="mt-1"
                    >
                      <Select.Trigger>
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox aria-label={intl.formatMessage({ id: "web.routes.backups.destination_options", defaultMessage: "Destination options", description: "Backups: accessible name of the list of backup destinations shown when the destination dropdown is open" })}>
                          {destinations.map((d) => (
                            <ListBoxItem key={d.name} id={d.name} textValue={d.name}>{d.name}</ListBoxItem>
                          ))}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                )}
                {noDestinations && (
                  <p className="text-xs text-foreground/60">{intl.formatMessage({ id: "web.routes.backups.no_backup_destinations_configured_add", defaultMessage: "No backup destinations configured. Add one in", description: "Backups: opening words of the note in the back-up-now dialog shown when no destination exists; the link and closing words follow it" })} <Link to="/admin" className="text-primary hover:underline">{intl.formatMessage({ id: "web.routes.backups.admin_settings", defaultMessage: "admin settings", description: "Backups: link text inside the no-destination note that opens the admin settings screen; keep it short" })}</Link> {intl.formatMessage({ id: "web.routes.backups.to_enable_snapshots", defaultMessage: "to enable snapshots.", description: "Backups: closing words of the no-destination note after the admin settings link, ending the sentence about enabling snapshots" })}</p>
                )}
                {createNow.error && <ErrorBanner err={createNow.error} />}
              </div>
            </ModalBody>
            <ModalFooter>
              <Button variant="ghost" onPress={onClose} isDisabled={createNow.isPending}>{intl.formatMessage({ id: "web.routes.backups.cancel", defaultMessage: "Cancel", description: "Backups: button that closes the back-up-now dialog without starting a snapshot; keep it short" })}</Button>
              <Button
                variant="primary"
                isDisabled={!selectedServer || !createDest || createNow.isPending || noDestinations}
                onPress={() => createNow.mutate()}
              >
                {createNow.isPending
                  ? intl.formatMessage({ id: "web.routes.backups.starting", defaultMessage: "Starting…", description: "Backups: label of the Run snapshot button while the request is in flight" })
                  : intl.formatMessage({ id: "web.routes.backups.run_snapshot", defaultMessage: "Run snapshot", description: "Backups: label of the button that starts a snapshot now" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

function SchedulesTabPanel({ location, onLocationChange, locations }: LocationFilterProps) {
  const intl = useOptionalIntl();
  const [search, setSearch] = useState("");
  const [server, setServer] = useState("");
  const qc = useQueryClient();
  const [creatingFor, setCreatingFor] = useState<string>("");
  const [deleting, setDeleting] = useState<Located<BackupSchedule> | null>(null);

  const { data: schedulesFleet, error: fleetError, isLoading: fleetLoading } = useQuery({
    queryKey: ["fleet", "schedules", location],
    queryFn: ({ signal }) => Fleet.schedules({ cluster: location || undefined }, signal),
    refetchInterval: 10000,
  });
  const schedules = useMemo(() => ({ items: (schedulesFleet?.items ?? []).map(located) }), [schedulesFleet]);
  const { data: serversFleet } = useQuery({
    queryKey: ["fleet", "servers", "", ""],
    queryFn: ({ signal }) => Fleet.servers({}, signal),
  });
  const serversList = { items: (serversFleet?.items ?? []).map(located) };

  const items = schedules?.items ?? [];
  const filtered = items.filter((item) => {
    if (server && !matchesServer(item, server, serversList.items)) return false;
    const q = search.trim().toLowerCase();
    return !q || `${item.metadata.name} ${item.spec.serverRef.name}`.toLowerCase().includes(q);
  });
  const selectedServer = serversList.items.find((item) => targetKey(item.fleetTarget) === creatingFor);

  const toggleSuspend = useMutation({
    mutationFn: ({ item, suspend }: { item: Located<BackupSchedule>; suspend: boolean }) =>
      createResourceClient(item.fleetTarget).Schedules.patchSpec(item.metadata.name, { suspend }, item.fleetTarget.namespace),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["fleet", "schedules"] }),
  });

  const remove = useMutation({
    mutationFn: (item: Located<BackupSchedule>) => createResourceClient(item.fleetTarget).Schedules.remove(item.metadata.name, item.fleetTarget.namespace),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet", "schedules"] });
      setDeleting(null);
    },
  });

  return (
    <div className="space-y-4">
      <FleetCoverage partial={schedulesFleet?.partial} issues={schedulesFleet?.issues} error={fleetError} label={intl.formatMessage({ id: "web.routes.backups.schedules", defaultMessage: "Schedules", description: "Backups: name of the schedules tab, also its table label and the title of its panel; keep it short" })} />
      {fleetLoading && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.backups.loading_schedules", defaultMessage: "Loading schedules…", description: "Backups: loading text on the schedules tab while schedule data is fetched" })}</p>}
      <BackupFilters
        location={location}
        onLocationChange={onLocationChange}
        locations={locations}
        search={search}
        onSearchChange={setSearch}
        server={server}
        onServerChange={setServer}
        servers={serversList.items}
        trailing={intl.formatMessage(TRAILING_COUNT.schedules, { shown: filtered.length, total: items.length })}
      />
      <Card className="p-4">
        <div className="flex items-end gap-2">
          <div className="flex-1">
            <label className="text-xs font-medium">{intl.formatMessage({ id: "web.routes.backups.new_schedule_for", defaultMessage: "New schedule for", description: "Backups: label above the server dropdown of the new schedule form" })}</label>
            <Select
              value={creatingFor}
              onChange={(v) => setCreatingFor(v as string)}
              placeholder={intl.formatMessage({ id: "web.routes.backups.select_a_server", defaultMessage: "Select a server…", description: "Backups: grey placeholder of the server dropdown, asking the user to pick a server in the backup dialog or the new schedule form" })}
              aria-label={intl.formatMessage({ id: "web.routes.backups.select_a_server_2", defaultMessage: "Select a server", description: "Backups: accessible name of the server dropdown in the new schedule form" })}
              className="mt-1"
            >
              <Select.Trigger>
                <Select.Value />
                <Select.Indicator className="ml-auto h-4 w-4" />
              </Select.Trigger>
              <Select.Popover>
                <ListBox aria-label={intl.formatMessage({ id: "web.routes.backups.server_options", defaultMessage: "Server options", description: "Backups: accessible name of the list of servers shown when the server dropdown is open" })}>
                  {(serversList?.items ?? []).filter((item) => hasPermission(item.fleetPermissions, "schedules:write")).map((s) => (
                    <ListBoxItem key={targetKey(s.fleetTarget)} id={targetKey(s.fleetTarget)} textValue={intl.formatMessage(SERVER_OPTION_LABEL.label, { name: s.metadata.name, target: targetLabel(s.fleetTarget) })}>
                      {intl.formatMessage(SERVER_OPTION_LABEL.label, { name: s.metadata.name, target: targetLabel(s.fleetTarget) })}
                    </ListBoxItem>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>
          </div>
        </div>
      </Card>

      {selectedServer && (
        <ScheduleForm target={selectedServer.fleetTarget} permissions={selectedServer.fleetPermissions} serverName={selectedServer.metadata.name} onClose={() => setCreatingFor("")} />
      )}
      {toggleSuspend.error && <ErrorBanner err={toggleSuspend.error} />}
      {remove.error && <ErrorBanner err={remove.error} />}

      <div className="rounded-lg border border-border bg-card overflow-hidden">
        <Table.Root>
          <Table.ScrollContainer>
            <Table.Content aria-label={intl.formatMessage({ id: "web.routes.backups.schedules", defaultMessage: "Schedules", description: "Backups: name of the schedules tab, also its table label and the title of its panel; keep it short" })} keyboardNavigationBehavior="arrow">
              <Table.Header>
                <Table.Column key="name" isRowHeader>{intl.formatMessage({ id: "web.routes.backups.name", defaultMessage: "Name", description: "Backups: column heading for the name of each backup, schedule or restore; keep it short" })}</Table.Column>
                <Table.Column key="server">{intl.formatMessage({ id: "web.routes.backups.server", defaultMessage: "Server", description: "Backups: column heading for the server a row belongs to, and the accessible name of the server dropdown in the backup dialog; keep it short" })}</Table.Column>
                <Table.Column key="cron">{intl.formatMessage({ id: "web.routes.backups.cron", defaultMessage: "Cron", description: "Backups: column heading for the cron expression of each schedule; keep it short" })}</Table.Column>
                <Table.Column key="lastrun">{intl.formatMessage({ id: "web.routes.backups.last_run", defaultMessage: "Last run", description: "Backups: column heading for when a schedule last ran; keep it short" })}</Table.Column>
                <Table.Column key="nextrun">{intl.formatMessage({ id: "web.routes.backups.next_run", defaultMessage: "Next run", description: "Backups: column heading for when a schedule runs next; keep it short" })}</Table.Column>
                <Table.Column key="active">{intl.formatMessage({ id: "web.routes.backups.active", defaultMessage: "Active", description: "Backups: column heading for the switch that turns a schedule on or off; keep it short" })}</Table.Column>
                <Table.Column key="actions" className="text-end" />
              </Table.Header>
              <Table.Body renderEmptyState={() => <>{items.length > 0
                ? intl.formatMessage({ id: "web.routes.backups.no_schedules_match_the_current_filters", defaultMessage: "No schedules match the current filters.", description: "Backups: schedules empty state when filters hide every schedule" })
                : schedulesFleet?.partial || fleetError
                  ? intl.formatMessage({ id: "web.routes.backups.no_schedule_data_available", defaultMessage: "No schedule data available.", description: "Backups: schedules empty state when the list could not be loaded" })
                  : intl.formatMessage({ id: "web.routes.backups.no_schedules_configured_yet", defaultMessage: "No schedules configured yet.", description: "Backups: schedules empty state when none exist" })}</>}>
                {filtered.map((s) => (
                  <Table.Row key={targetKey(s.fleetTarget)}>
                    <Table.Cell>
                      <span className="font-mono text-xs">{s.metadata.name}</span><div className="text-xs text-muted">{targetLabel(s.fleetTarget)}</div>
                    </Table.Cell>
                    <Table.Cell>{s.spec.serverRef.name}</Table.Cell>
                    <Table.Cell>
                      <span className="font-mono text-xs">{s.spec.schedule}</span>
                    </Table.Cell>
                    <Table.Cell className="text-foreground/60">
                      {formatRelative(s.status?.lastSuccessfulTime, intl)}
                    </Table.Cell>
                    <Table.Cell className="text-foreground/60">
                      {s.spec.suspend ? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" }) : formatRelativeFuture(s.status?.nextScheduleTime, intl)}
                    </Table.Cell>
                    <Table.Cell>
                      <Switch
                        isSelected={!s.spec.suspend}
                        isDisabled={!hasPermission(s.fleetPermissions, "schedules:write") || toggleSuspend.isPending}
                        onChange={(isSelected) =>
                          toggleSuspend.mutate({ item: s, suspend: !isSelected })
                        }
                        aria-label={intl.formatMessage({ id: "web.routes.backups.schedule_active", defaultMessage: "Schedule active", description: "Backups: accessible name of the switch that turns a schedule on or off" })}
                      >
                        <Switch.Content>
                          <Switch.Control>
                            <Switch.Thumb />
                          </Switch.Control>
                        </Switch.Content>
                      </Switch>
                    </Table.Cell>
                    <Table.Cell>
                      <Button
                        size="sm"
                        variant="ghost"
                        isDisabled={!hasPermission(s.fleetPermissions, "schedules:write")}
                        onPress={() => setDeleting(s)}
                      >{intl.formatMessage({ id: "web.routes.backups.delete", defaultMessage: "Delete", description: "Backups: button that starts deleting a schedule row, and the confirm button of the delete schedule dialog; keep it short" })}</Button>
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table.Root>
      </div>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={intl.formatMessage({ id: "web.routes.backups.delete_schedule", defaultMessage: "Delete schedule?", description: "Backups: title of the dialog asking whether to delete a schedule" })}
        description={
          <>
            <p>{intl.formatMessage({ id: "web.routes.backups.the_cron_schedule_will_be_removed", defaultMessage: "The cron schedule will be removed. Existing backups remain intact; new snapshots on this schedule will stop running until it's recreated.", description: "Backups: text in the delete schedule dialog: the schedule is removed, existing backups stay, and new snapshots stop until it is recreated" })}</p>
            {deleting && (
              <p className="pt-2">{intl.formatMessage({ id: "web.routes.backups.type_name_to_confirm", defaultMessage: "Type {name} ({target}) to confirm.", description: "Backups: confirmation hint under the delete-schedule dialog; {name} is the schedule name in monospace, {target} is its cluster and namespace" }, { name: <span className="font-mono">{deleting.metadata.name}</span>, target: targetLabel(deleting.fleetTarget) })}</p>
            )}
          </>
        }
        confirmPhrase={deleting?.metadata.name}
        confirmLabel={intl.formatMessage({ id: "web.routes.backups.delete", defaultMessage: "Delete", description: "Backups: button that starts deleting a schedule row, and the confirm button of the delete schedule dialog; keep it short" })}
        destructive
        busy={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  );
}

function RestoresTabPanel({ location, onLocationChange, locations }: LocationFilterProps) {
  const intl = useOptionalIntl();
  const [server, setServer] = useState("");
  const [phase, setPhase] = useState("");
  const [search, setSearch] = useState("");

  const { data: restoresFleet, error: fleetError, isLoading: fleetLoading } = useQuery({
    queryKey: ["fleet", "restores", location],
    queryFn: ({ signal }) => Fleet.restores({ cluster: location || undefined }, signal),
    refetchInterval: 5000,
  });
  const restores = useMemo(() => ({ items: (restoresFleet?.items ?? []).map(located) }), [restoresFleet]);
  const { data: serversFleet } = useQuery({
    queryKey: ["fleet", "servers", "", ""],
    queryFn: ({ signal }) => Fleet.servers({}, signal),
  });
  const serversList = { items: (serversFleet?.items ?? []).map(located) };

  const items = useMemo(() => restores?.items ?? [], [restores]);
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return items.filter((r) => {
      if (server && !matchesServer(r, server, serversList.items)) return false;
      if (phase && r.status?.phase !== phase) return false;
      if (q) {
        const hay = `${r.metadata.name} ${r.spec.serverRef.name} ${r.spec.backupRef.name}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [items, search, server, phase, serversList.items]);

  return (
    <div className="space-y-4">
      <FleetCoverage partial={restoresFleet?.partial} issues={restoresFleet?.issues} error={fleetError} label={intl.formatMessage({ id: "web.routes.backups.restores", defaultMessage: "Restores", description: "Backups: name of the restores tab, also its table label; keep it short" })} />
      {fleetLoading && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.backups.loading_restores", defaultMessage: "Loading restores…", description: "Backups: loading text on the restores tab while restore data is fetched" })}</p>}
      <BackupFilters
        location={location}
        onLocationChange={onLocationChange}
        locations={locations}
        search={search}
        onSearchChange={setSearch}
        server={server}
        onServerChange={setServer}
        phase={phase}
        onPhaseChange={setPhase}
        servers={serversList?.items ?? []}
        phases={RESTORE_PHASES}
        trailing={intl.formatMessage(TRAILING_COUNT.restores, { shown: filtered.length, total: items.length })}
      />
      <div className="rounded-lg border border-border bg-card overflow-hidden">
        <Table.Root>
          <Table.ScrollContainer>
            <Table.Content aria-label={intl.formatMessage({ id: "web.routes.backups.restores", defaultMessage: "Restores", description: "Backups: name of the restores tab, also its table label; keep it short" })} keyboardNavigationBehavior="arrow">
              <Table.Header>
                <Table.Column key="name" isRowHeader>{intl.formatMessage({ id: "web.routes.backups.name", defaultMessage: "Name", description: "Backups: column heading for the name of each backup, schedule or restore; keep it short" })}</Table.Column>
                <Table.Column key="backup">{intl.formatMessage({ id: "web.routes.backups.backup", defaultMessage: "Backup", description: "Backups: column heading for the backup a restore came from; keep it short" })}</Table.Column>
                <Table.Column key="target">{intl.formatMessage({ id: "web.routes.backups.target", defaultMessage: "Target", description: "Backups: column heading for the target a restore was applied to; keep it short" })}</Table.Column>
                <Table.Column key="phase">{intl.formatMessage({ id: "web.routes.backups.phase", defaultMessage: "Phase", description: "Backups: column heading for the phase or state of a backup or restore; keep it short" })}</Table.Column>
                <Table.Column key="completed">{intl.formatMessage({ id: "web.routes.backups.completed", defaultMessage: "Completed", description: "Backups: column heading for when a backup or restore completed; keep it short" })}</Table.Column>
                <Table.Column key="message">{intl.formatMessage({ id: "web.routes.backups.message", defaultMessage: "Message", description: "Backups: column heading for the status message of a restore; keep it short" })}</Table.Column>
              </Table.Header>
              <Table.Body
                renderEmptyState={() => (
                  <>
                    {items.length === 0
                      ? (restoresFleet?.partial || fleetError
                        ? intl.formatMessage({ id: "web.routes.backups.no_restore_data_available", defaultMessage: "No restore data available.", description: "Backups: restores empty state when the list could not be loaded" })
                        : intl.formatMessage({ id: "web.routes.backups.no_restores_have_been_run", defaultMessage: "No restores have been run.", description: "Backups: restores empty state when no restore has run" }))
                      : intl.formatMessage({ id: "web.routes.backups.no_restores_match_the_current_filters", defaultMessage: "No restores match the current filters.", description: "Backups: restores empty state when filters hide every restore" })}
                  </>
                )}
              >
                {filtered.map((r) => (
                  <Table.Row key={targetKey(r.fleetTarget)}>
                    <Table.Cell>
                      <span className="font-mono text-xs">{r.metadata.name}</span><div className="text-xs text-muted">{targetLabel(r.fleetTarget)}</div>
                    </Table.Cell>
                    <Table.Cell>
                      <span className="font-mono text-xs">{r.spec.backupRef.name}</span>
                    </Table.Cell>
                    <Table.Cell>{r.spec.serverRef.name}</Table.Cell>
                    <Table.Cell>
                      <PhaseChip phase={r.status?.phase} />
                    </Table.Cell>
                    <Table.Cell className="text-foreground/60">
                      {formatRelative(r.status?.completionTime, intl)}
                    </Table.Cell>
                    <Table.Cell className="text-xs text-foreground/60">
                      {r.status?.message ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table.Root>
      </div>
    </div>
  );
}

function hasPermission(permissions: string[] | undefined, permission: string): boolean {
  return permissions?.includes(permission) === true || permissions?.includes("*") === true;
}

function matchesServer(resource: Located<Backup> | Located<BackupSchedule> | Located<import("@/types").Restore>, key: string, servers: Located<import("@/types").GameServer>[]): boolean {
  const server = servers.find((item) => targetKey(item.fleetTarget) === key);
  return !!server && resource.fleetTarget.cluster === server.fleetTarget.cluster && resource.fleetTarget.namespace === server.fleetTarget.namespace && resource.spec.serverRef.name === server.metadata.name;
}
