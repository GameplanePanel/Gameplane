import { lazy, Suspense, useState, useEffect } from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Tab, Tabs } from "@heroui/react";
import {
  AlertTriangle,
  Loader2,
  Play,
  RotateCw,
  Square,
  Sunrise,
  Terminal,
} from "lucide-react";
import { createResourceClient, type LifecycleVerb } from "@/lib/endpoints";
import { useGameCodes } from "@/lib/useGameCodes";
import { resolveConsoleMode, serverHasMods, serverHasModpacks } from "@/lib/capabilities";
import { useMe } from "@/lib/auth";
import { PhaseChip } from "@/components/ui/PhaseChip";
import { GameIcon } from "@/components/ui/GameIcon";
import { capitalize, formatUptime, ignoreRejection } from "@/lib/utils";
import { ServerActionsMenu } from "@/components/server/ServerActionsMenu";
import { CaptureWidget } from "@/components/CaptureWidget";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { ResourceTargetProvider, useResourceClient, useResourceTarget, useResourceAccess, resourceCan, resourceKey, serverLink, type ResourceTarget, type ServerAccessResponse } from "@/lib/resourceTarget";
import { createRequestClient } from "@/lib/api";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";

import { OverviewTab } from "./tabs/Overview";
import { EventsTab } from "./tabs/Events";
import { LogsTab } from "./tabs/Logs";
import { ModsTab } from "./tabs/Mods";
import { ModpacksTab } from "./tabs/Modpacks";
import { PlayersTab } from "./tabs/Players";
import { BackupsTab } from "./tabs/Backups";
import { SettingsTab } from "./tabs/Settings";
import { useOptionalIntl } from "@/lib/i18n/format";
import { conditionMessageText } from "@/lib/i18n/conditionMessage";
import { defineMessages, type MessageDescriptor } from "react-intl";

// Console (xterm) and Files (Monaco) bring in heavy vendor deps; load on demand.
const ConsoleTab = lazy(() =>
  import("./tabs/Console").then((m) => ({ default: m.ConsoleTab })),
);
const FilesTab = lazy(() =>
  import("./tabs/Files").then((m) => ({ default: m.FilesTab })),
);

type TabKey =
  | "overview" | "events" | "console" | "logs" | "files" | "mods" | "modpacks" | "players" | "backups" | "capture" | "settings";

const TAB_LABELS = defineMessages({
  overview: { id: "web.routes.server_detail.tab_overview", defaultMessage: "Overview", description: "Server detail: label of the Overview tab" },
  events: { id: "web.routes.server_detail.tab_events", defaultMessage: "Events", description: "Server detail: label of the Events tab" },
  console: { id: "web.routes.server_detail.tab_console", defaultMessage: "Console", description: "Server detail: label of the Console tab" },
  logs: { id: "web.routes.server_detail.tab_logs", defaultMessage: "Logs", description: "Server detail: label of the Logs tab" },
  files: { id: "web.routes.server_detail.tab_files", defaultMessage: "Files", description: "Server detail: label of the Files tab" },
  mods: { id: "web.routes.server_detail.tab_mods", defaultMessage: "Mods", description: "Server detail: label of the Mods tab" },
  modpacks: { id: "web.routes.server_detail.tab_modpacks", defaultMessage: "Modpacks", description: "Server detail: label of the Modpacks tab" },
  players: { id: "web.routes.server_detail.tab_players", defaultMessage: "Players", description: "Server detail: label of the Players tab" },
  backups: { id: "web.routes.server_detail.tab_backups", defaultMessage: "Backups", description: "Server detail: label of the Backups tab" },
  capture: { id: "web.routes.server_detail.tab_capture", defaultMessage: "Capture", description: "Server detail: label of the Capture tab" },
  settings: { id: "web.routes.server_detail.tab_settings", defaultMessage: "Settings", description: "Server detail: label of the Settings tab" },
});

const tabs: Array<{ key: TabKey; label: MessageDescriptor }> = [
  { key: "overview", label: TAB_LABELS.overview },
  { key: "events",   label: TAB_LABELS.events },
  { key: "console",  label: TAB_LABELS.console },
  { key: "logs",     label: TAB_LABELS.logs },
  { key: "files",    label: TAB_LABELS.files },
  { key: "mods",     label: TAB_LABELS.mods },
  { key: "modpacks", label: TAB_LABELS.modpacks },
  { key: "players",  label: TAB_LABELS.players },
  { key: "backups",  label: TAB_LABELS.backups },
  { key: "capture",  label: TAB_LABELS.capture },
  { key: "settings", label: TAB_LABELS.settings },
];

export function ServerDetailPage() {
  const { name } = useParams({ from: "/app-layout/servers/$name" });
  const { ns, cluster = "local" } = useSearch({ from: "/app-layout/servers/$name" });
  return <ResolveServerDetail key={JSON.stringify([cluster, ns, name])} target={{ cluster, namespace: ns, name }} />;
}

function ResolveServerDetail({ target }: { target: ResourceTarget }) {
  const intl = useOptionalIntl();
  const navigate = useNavigate();
  const server = useQuery({
    queryKey: resourceKey(target, "server"),
    queryFn: ({ signal }) => createResourceClient(target, signal).Servers.get(target.name, target.namespace),
  });
  const resolved = { ...target, namespace: server.data?.metadata.namespace ?? target.namespace, uid: server.data?.metadata.uid };
  const access = useQuery({
    queryKey: resourceKey(resolved, "access"),
    queryFn: ({ signal }) => createRequestClient(resolved, signal).api<ServerAccessResponse>(
      `/servers/${encodeURIComponent(target.name)}/access`,
    ),
    enabled: !!server.data,
  });
  useEffect(() => {
    if (!target.namespace && server.data?.metadata.namespace) {
      void navigate({ ...serverLink({ cluster: target.cluster, name: target.name, namespace: server.data.metadata.namespace }), replace: true });
    }
  }, [target.cluster, target.namespace, target.name, server.data?.metadata.namespace, navigate]);
  const retry = () => { void server.refetch(); void access.refetch(); };
  if (server.error || access.error) return <div className="p-6"><ErrorCard message={intl.formatMessage({ id: "web.routes.server_detail.this_server_could_not_be_loaded_at_its", defaultMessage: "This server could not be loaded at its recorded location. Check your access and the site's connection.", description: "Server detail: error card shown when the server or the user's access to it cannot be loaded at its recorded location; a retry button is on the card" })} onRetry={retry} /></div>;
  if (!server.data || !access.data) return <div className="p-6"><LoadingCard message={intl.formatMessage({ id: "web.routes.server_detail.loading_server", defaultMessage: "Loading server…", description: "Server detail: loading text shown while the server and the user's access to it are being fetched" })} /></div>;
  const authorized = access.data.target;
  if (authorized.cluster !== resolved.cluster || authorized.namespace !== resolved.namespace || authorized.name !== resolved.name || (resolved.uid && authorized.uid !== resolved.uid)) {
    return <div className="p-6"><ErrorCard message={intl.formatMessage({ id: "web.routes.server_detail.this_server_changed_while_it_was_being", defaultMessage: "This server changed while it was being loaded. Reload its current identity before continuing.", description: "Server detail: error card shown when the server's identity no longer matches the one the link was for; asks the user to reload its current identity" })} onRetry={retry} /></div>;
  }
  return (
    <ResourceTargetProvider target={resolved} access={access.data}>
      <ServerDetailView key={JSON.stringify(resourceKey(resolved))} lookupTarget={target} />
    </ResourceTargetProvider>
  );
}

function ServerDetailView({ lookupTarget }: { lookupTarget: ResourceTarget }) {
  const intl = useOptionalIntl();
  const target = useResourceTarget(lookupTarget);
  const clusterId = target.cluster;
  const name = target.name;
  const ns = target.namespace;
  const client = useResourceClient();
  const { Servers } = client;
  const access = useResourceAccess();
  const [tab, setTab] = useState<TabKey>("overview");
  const [settingsDirty, setSettingsDirty] = useState(false);
  const qc = useQueryClient();
  const nav = useNavigate();
  const { data: me } = useMe();

  const { data: gs } = useQuery({
    queryKey: resourceKey(lookupTarget, "server"),
    queryFn: ({ signal }) => client.withSignal(signal).Servers.get(name, ns),
    refetchInterval: settingsDirty ? false : 5_000,
  });

  const templateName = gs?.spec.templateRef.name;
  const { data: tmpl } = useQuery({
    queryKey: resourceKey(target, "template", templateName),
    queryFn: ({ signal }) => client.withSignal(signal).Templates.get(templateName as string),
    enabled: !!templateName,
  });

  const { gameCodes } = useGameCodes(clusterId);

  const act = useMutation({
    mutationFn: (verb: LifecycleVerb) => Servers.lifecycle(name, verb, ns, clusterId),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: resourceKey(lookupTarget, "server") });
      await qc.invalidateQueries({ queryKey: ["fleet"] });
    },
  });

  const phase = gs?.status?.phase;
  // The operator's derivePhase reports Stopping (not Suspended) for the
  // whole graceful-drain window even after status.idle.asleep flips true —
  // players may still be connected. Excluding Stopping keeps the badge
  // honest and keeps the Wake button from appearing on a server that is
  // still shutting down, not actually asleep yet.
  const asleep = gs?.status?.idle?.asleep === true && phase !== "Stopping";
  const running = phase === "Running";
  // While provisioning, the operator refines the Progressing condition
  // with what the pod is doing (pulling image, installing server files,
  // waiting for the agent) — surface it under the phase badge.
  const provisioning = phase === "Starting" || phase === "Pending";
  const progressMessage = conditionMessageText(
    intl,
    gs?.status?.conditions?.find((c) => c.type === "Progressing"),
    gs?.status?.conditionArgs,
  );
  // When startup terminally fails the operator sets phase=Failed and puts
  // the reason on the Ready condition (image pull, crash-loop, exit). Both
  // the operator's escalation and a build-error setPhase write Ready, so
  // read the failure message from there.
  const failed = phase === "Failed";
  const failureMessage = conditionMessageText(
    intl,
    gs?.status?.conditions?.find((c) => c.type === "Ready"),
    gs?.status?.conditionArgs,
  );
  // Gate lifecycle actions on phase so they aren't fired during a
  // transition (Starting/Stopping/Pending): Start only from a stopped
  // state, Stop/Restart only while Running. act.isPending blocks
  // duplicate mutations from a double-click. When asleep, Start is a silent
  // no-op (spec.suspend is already false) — only :wake clears the operator's
  // sleep marker.
  const canStart = (phase === "Stopped" || phase === "Suspended" || phase === "Failed") && !asleep;
  const version = gs?.status?.agent?.gameVersion;
  const uptime = formatUptime(gs?.status?.startedAt, intl);

  // Tab visibility is driven by the template: a game with no console
  // (consoleMode none / no RCON) hides the Console tab. Logs is always
  // available — the tab streams the container's stdout (install/startup
  // output) via the pod-log API, which needs no logPath; the configured
  // game-log file is just an extra source the tab offers when logPath is
  // set. While the template is still loading we show everything.
  const consoleAvailable = !!access?.canConsole && (!tmpl || resolveConsoleMode(tmpl) !== "none");
  // Mods only appears when this server actually has a mod directory — the
  // template declares the capability AND (for the per-loader model) the
  // active version's loader maps to one. Hidden for e.g. vanilla servers.
  const modsAvailable = serverHasMods(tmpl, gs);
  // Modpacks appears when the template offers modpacks AND the active
  // version's loader can run one — hidden for vanilla and plugin loaders
  // (e.g. Paper), which can't load a Modrinth/Forge modpack.
  const modpacksAvailable = serverHasModpacks(tmpl, gs);
  // Hide Capture only on a confirmed denial. While /users/me is loading or
  // after it failed, permissions are unknown: keep the tab reachable so the
  // widget can show its checking / identity-unavailable (retry) state, with
  // the capture controls still closed.
  const captureTabVisible = !me || resourceCan(access?.permissions ?? [], "captures:manage");
  const visibleTabs = tabs.filter((t) => {
    if (t.key === "console") return consoleAvailable;
    if (t.key === "mods") return modsAvailable;
    if (t.key === "modpacks") return modpacksAvailable;
    if (t.key === "capture") return captureTabVisible;
    return true;
  });

  // If the active tab gets hidden once the template resolves, fall back
  // to Overview so the content area never goes blank. Adjusted directly
  // during render (not in an effect) — "overview" is always visible, so
  // this can't loop.
  if (!visibleTabs.some((t) => t.key === tab)) setTab("overview");

  // A freshly-created server is provisioning — land the user on Logs so
  // they can watch the install stream, rather than an empty Overview.
  // "Never been Running" = Pending/Starting with no startedAt (the
  // operator sets startedAt only on first reaching Running). Fires once,
  // after gs first loads, and never fights a manual tab click. Adjusted
  // directly during render (not in an effect), gated on autoTabApplied.
  const [autoTabApplied, setAutoTabApplied] = useState(false);
  if (!autoTabApplied && gs) {
    setAutoTabApplied(true);
    const st = gs.status;
    if ((st?.phase === "Pending" || st?.phase === "Starting") && !st.startedAt) {
      setTab("logs");
    }
  }

  return (
    <div className="flex h-full flex-col">
      <header className="border-b border-border bg-background px-6 pb-4 pt-8">
        <div className="flex flex-wrap items-start justify-between gap-4 pb-8">
          <div className="flex items-start gap-4">
            <GameIcon
              game={gs?.spec.templateRef.name}
              icon={tmpl?.spec.icon}
              code={gameCodes.get(gs?.spec.templateRef.name ?? "")}
              accentColor={tmpl?.spec.accentColor}
              size="lg"
            />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-3">
                <h1 className="truncate font-mono text-2xl font-semibold text-foreground">{name}</h1>
                <PhaseChip phase={phase} asleep={asleep} />
              </div>
              {provisioning && progressMessage && (
                <div className="pt-1 flex items-center gap-1.5 text-xs text-warning">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  <span>{capitalize(progressMessage)}</span>
                </div>
              )}
              {failed && (
                <div className="pt-1 flex items-center gap-1.5 text-xs text-danger">
                  <AlertTriangle className="h-3 w-3" />
                  <span>
                    {failureMessage ? capitalize(failureMessage) : intl.formatMessage({ id: "web.routes.server_detail.server_failed_to_start", defaultMessage: "The server failed to start.", description: "Server detail: shown when the server failed to start and gives no reason" })}
                  </span>
                </div>
              )}
              <div className="pt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
                <span>{intl.formatMessage({ id: "web.routes.server_detail.site_cluster", defaultMessage: "site: {cluster}", description: "Server detail: header line naming the cluster; {cluster} is the cluster name" }, { cluster: target.cluster })}</span><Dot />
                {gs?.spec.templateRef.name && <span>{gs.spec.templateRef.name}</span>}
                {version && <Dot />}{version && <span>{version}</span>}
                {gs?.metadata.namespace && <><Dot /><span>{intl.formatMessage({ id: "web.routes.server_detail.ns_namespace", defaultMessage: "ns: {namespace}", description: "Server detail: header line naming the namespace; {namespace} is the namespace name" }, { namespace: gs.metadata.namespace })}</span></>}
                {uptime !== "—" && <><Dot /><span>{intl.formatMessage({ id: "web.routes.server_detail.up_uptime", defaultMessage: "up {uptime}", description: "Server detail: header line with uptime; {uptime} is the formatted uptime" }, { uptime: uptime })}</span></>}
              </div>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              className="rounded-full"
              onPress={() => act.mutate("restart")}
              isDisabled={!access?.canControl || !running || act.isPending}
            >
              <RotateCw className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.server_detail.restart", defaultMessage: "Restart", description: "Server detail: header button that restarts the running server; disabled without control access; keep it short" })}</Button>
            <Button
              variant="outline"
              className="rounded-full"
              onPress={() => act.mutate("stop")}
              isDisabled={!access?.canControl || (!running && !asleep) || act.isPending}
            >
              <Square className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.server_detail.stop", defaultMessage: "Stop", description: "Server detail: header button that stops the running server, or puts a sleeping server to sleep fully; keep it short" })}</Button>
            {asleep && (
              <Button
                variant="primary"
                className="rounded-full"
                onPress={() => act.mutate("wake")}
                isDisabled={!access?.canControl || act.isPending}
              >
                <Sunrise className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.server_detail.wake", defaultMessage: "Wake", description: "Server detail: header button that wakes a sleeping server; disabled without control access; keep it short" })}</Button>
            )}
            {canStart && (
              <Button
                variant="outline"
                className="rounded-full"
                onPress={() => act.mutate("start")}
                isDisabled={act.isPending}
              >
                <Play className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.server_detail.start", defaultMessage: "Start", description: "Server detail: header button that starts a stopped server; keep it short" })}</Button>
            )}
            {consoleAvailable && (
              <Button className="rounded-full" onPress={() => setTab("console")}>
                <Terminal className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.server_detail.open_console", defaultMessage: "Open console", description: "Server detail: header button that switches to the console tab; shown only when a console is available; keep it short" })}</Button>
            )}
            {gs && (
              <ServerActionsMenu
                gs={gs}
                onDeleted={() => ignoreRejection(nav({ to: "/servers" }))}
              />
            )}
          </div>
        </div>

        {act.error && (
          <div className="pb-4">
            <ErrorBanner err={act.error} onDismiss={() => act.reset()} />
          </div>
        )}

        <nav className="overflow-x-auto scrollbar-thin">
          <Tabs
            selectedKey={tab}
            onSelectionChange={(key) => setTab(key as TabKey)}
            className="w-full"
          >
            <Tabs.List aria-label={intl.formatMessage({ id: "web.routes.server_detail.server_detail_tabs", defaultMessage: "Server detail tabs", description: "Server detail: accessible label of the row of server tabs, read by screen readers; not shown as text" })} className="flex w-full gap-1 overflow-x-auto">
              {visibleTabs.map((t) => (
                <Tab key={t.key} id={t.key}>
                  {intl.formatMessage(t.label)}
                </Tab>
              ))}
            </Tabs.List>
          </Tabs>
        </nav>
      </header>

      <div className="flex-1 overflow-auto scrollbar-thin">
        <Suspense fallback={<TabFallback />}>
          {tab === "overview" && (
            <OverviewTab
              gs={gs}
              name={name}
              tmpl={tmpl}
              ns={ns}
              onViewAllEvents={() => setTab("events")}
              onOpenConsole={consoleAvailable ? () => setTab("console") : undefined}
            />
          )}
          {tab === "events"   && <EventsTab name={name} ns={ns} gs={gs} />}
          {tab === "console"  && <ConsoleTab name={name} ns={ns} />}
          {tab === "logs"     && (
            <LogsTab
              name={name}
              ns={ns}
              logPath={tmpl?.spec.logPath}
              phase={phase}
              progressMessage={progressMessage}
            />
          )}
          {tab === "files"    && <FilesTab   name={name} ns={ns} />}
          {tab === "mods"     && <ModsTab    name={name} ns={ns} tmpl={tmpl} gs={gs} />}
          {tab === "modpacks" && <ModpacksTab name={name} ns={ns} tmpl={tmpl} />}
          {tab === "players"  && <PlayersTab name={name} ns={ns} />}
          {tab === "backups"  && <BackupsTab name={name} ns={ns} />}
          {tab === "capture"  && <CaptureWidget name={name} ns={ns} gs={gs} />}
          {tab === "settings" && (
            <SettingsTab gs={gs} name={name} ns={ns} onDirtyChange={setSettingsDirty} />
          )}
        </Suspense>
      </div>
    </div>
  );
}

function Dot() {
  const intl = useOptionalIntl();
  return <span className="text-muted/50">{intl.formatMessage({ id: "web.common.separator", defaultMessage: "·", description: "Separator character between items in a list, such as wizard steps" })}</span>;
}

function TabFallback() {
  const intl = useOptionalIntl();
  return (
    <div className="flex h-full items-center justify-center text-sm text-muted">{intl.formatMessage({ id: "web.routes.server_detail.loading", defaultMessage: "Loading…", description: "Server detail: loading text shown in the body of a tab while that tab's content is still loading" })}</div>
  );
}
