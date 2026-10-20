import { useResourceClient, useResourceTarget } from "@/lib/resourceTarget";
import { useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { AlertTriangle, Download, Loader2 } from "lucide-react";
import { Button, Input, Tabs, Tab as TabComponent } from "@heroui/react";



import { useFormat } from "@/lib/i18n/format";
import { openWS } from "@/lib/ws";
import { capitalize, cn } from "@/lib/utils";
import type { GameServerPhase } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

const MAX_LINES = 20_000;

// Best-effort client-side log-level extraction. Game logs vary, but most
// emit a recognizable level token (e.g. Minecraft "[Server thread/INFO]");
// this drives filtering + row coloring only, never parsing semantics, and
// returns null when no level is found.
export type LogLevel = "ERROR" | "WARN" | "INFO" | "DEBUG";
const LEVEL_RE = /\b(ERROR|ERR|SEVERE|FATAL|WARNING|WARN|INFO|DEBUG|TRACE|FINE)\b/i;
export function parseLogLevel(line: string): LogLevel | null {
  const m = LEVEL_RE.exec(line);
  if (!m) return null;
  const t = m[1].toUpperCase();
  if (t === "ERROR" || t === "ERR" || t === "SEVERE" || t === "FATAL") return "ERROR";
  if (t === "WARN" || t === "WARNING") return "WARN";
  if (t === "DEBUG" || t === "TRACE" || t === "FINE") return "DEBUG";
  return "INFO";
}
const LEVELS: LogLevel[] = ["INFO", "WARN", "ERROR", "DEBUG"];
const LEVEL_CLASS: Record<LogLevel, string> = {
  ERROR: "text-red-400",
  WARN: "text-amber-400",
  INFO: "",
  DEBUG: "text-muted",
};
// Tab labels of the level filter; "all" is the filter that shows every level.
const LEVEL_TABS: Record<"all" | LogLevel, MessageDescriptor> = defineMessages({
  all: { id: "web.routes.tabs.logs.level_all", defaultMessage: "All", description: "Logs: tab label of the log level filter that shows every level" },
  INFO: { id: "web.routes.tabs.logs.level_info", defaultMessage: "Info", description: "Logs: tab label of the INFO log level filter" },
  WARN: { id: "web.routes.tabs.logs.level_warn", defaultMessage: "Warn", description: "Logs: tab label of the WARN log level filter" },
  ERROR: { id: "web.routes.tabs.logs.level_error", defaultMessage: "Error", description: "Logs: tab label of the ERROR log level filter" },
  DEBUG: { id: "web.routes.tabs.logs.level_debug", defaultMessage: "Debug", description: "Logs: tab label of the DEBUG log level filter" },
});

// Log sources the tab streams from. "pod" follows the whole pod timeline —
// each setup/init container's output, then the game container's stdout —
// so the install/setup step is visible while a server is still "Starting"
// (before it writes its own log file), and it works without agent mTLS;
// "file" tails the configured game log file via the agent.
type LogSource = "pod" | "file";

export function LogsTab({
  name,
  ns,
  logPath,
  phase,
  progressMessage,
}: {
  name: string;
  ns?: string;
  logPath?: string;
  phase?: GameServerPhase;
  progressMessage?: string;
}) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Logs } = resourceClient;
  const fmt = useFormat();

  const [lines, setLines] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [level, setLevel] = useState<"all" | LogLevel>("all");
  // Default to container output so logs are never empty during startup.
  const [source, setSource] = useState<LogSource>("pod");
  const [connected, setConnected] = useState(false);
  // The agent-backed game-log stream can fail to connect (e.g. the agent
  // is unreachable); track that so we can show an actionable notice instead
  // of an endless "Connecting…" spinner.
  const [streamFailed, setStreamFailed] = useState(false);
  const scrollerRef = useRef<HTMLDivElement | null>(null);

  // The game-log file is only an option when the template declares a
  // logPath; otherwise the only source is the container's stdout, which
  // is always available via the pod-log API.
  const effectiveSource: LogSource = logPath ? source : "pod";

  useEffect(() => {
    setLines([]);
    setConnected(false);
    setStreamFailed(false);
    const path =
      effectiveSource === "pod" ? Logs.podStreamPath(name, ns) : Logs.fileStreamPath(name, ns);
    const sock = openWS(path, {
      onOpen: () => setConnected(true),
      onClose: () => setConnected(false),
      onStatus: (status, info) => {
        if (status === "open") {
          setStreamFailed(false);
          // Only the agent-backed file stream surfaces an actionable
          // failure: the user can fall back to container output. The pod
          // stream's retries are expected during provisioning, so leave
          // those to the provisioning placeholder.
        } else if (effectiveSource === "file" && status === "reconnecting" && info.attempt >= 2) {
          setStreamFailed(true);
        }
      },
      onMessage: (data) =>
        setLines((prev) => {
          const next = prev.concat(typeof data === "string" ? data.split(/\r?\n/) : []);
          return next.length > MAX_LINES ? next.slice(-MAX_LINES) : next;
        }),
    });
    return () => sock.close();
  }, [name, ns, effectiveSource, Logs]);

  // Parse each line's level once; derive counts + the filtered view from it.
  const parsed = useMemo(
    () => lines.map((text) => ({ text, level: parseLogLevel(text) })),
    [lines],
  );
  const counts = useMemo(() => {
    const c: Record<LogLevel, number> = { ERROR: 0, WARN: 0, INFO: 0, DEBUG: 0 };
    for (const p of parsed) if (p.level) c[p.level]++;
    return c;
  }, [parsed]);
  const needle = filter.toLowerCase();
  const filtered = useMemo(
    () =>
      parsed.filter((p) => {
        if (needle && !p.text.toLowerCase().includes(needle)) return false;
        if (level !== "all" && p.level !== level) return false;
        return true;
      }),
    [parsed, needle, level],
  );

  const rowVirtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => scrollerRef.current,
    estimateSize: () => 18,
    overscan: 20,
  });

  useEffect(() => {
    // Autoscroll to bottom on new lines.
    const el = scrollerRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines.length]);

  // No output yet. While the server is still coming up (image pull /
  // first-boot install), the pod-log stream stays closed (the API replies
  // StatusTryAgainLater and openWS retries), so an empty black panel reads
  // as "broken". Show what's actually happening instead.
  const noOutput = lines.length === 0;
  const failed = phase === "Failed";
  const provisioning = phase !== undefined && phase !== "Running" && !failed;
  // The game-log file stream couldn't reach the agent and produced nothing —
  // offer container output (always available) instead of spinning forever.
  const fileUnavailable = effectiveSource === "file" && streamFailed && noOutput;

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-border px-4 py-2">
        {logPath ? (
          <Tabs selectedKey={source} onSelectionChange={(key) => setSource(key as LogSource)} variant="secondary" aria-label={intl.formatMessage({ id: "web.routes.tabs.logs.log_source", defaultMessage: "Log source", description: "Logs tab: accessible label of the tabs that choose between container output and game log; not shown as text" })}>
            <Tabs.List>
              <TabComponent id="pod">{intl.formatMessage({ id: "web.routes.tabs.logs.container_output", defaultMessage: "Container output", description: "Logs tab: tab name for the container output of the pod, the setup and startup output; when no log file exists it is shown as plain text" })}</TabComponent>
              <TabComponent id="file">{intl.formatMessage({ id: "web.routes.tabs.logs.game_log", defaultMessage: "Game log", description: "Logs tab: tab name for the game log file written by the game itself" })}</TabComponent>
            </Tabs.List>
          </Tabs>
        ) : (
          <span
            className="text-xs font-medium text-muted"
            title={intl.formatMessage({ id: "web.routes.tabs.logs.follow_the_pod_s_setup_game_container", defaultMessage: "Follow the pod's setup + game container output (install/startup)", description: "Logs tab: tooltip on the plain container output label explaining it holds the pod setup and game container output for install and startup" })}
          >{intl.formatMessage({ id: "web.routes.tabs.logs.container_output", defaultMessage: "Container output", description: "Logs tab: tab name for the container output of the pod, the setup and startup output; when no log file exists it is shown as plain text" })}</span>
        )}
        <div className="w-64 shrink-0">
          <Input
            placeholder={intl.formatMessage({ id: "web.routes.tabs.logs.filter", defaultMessage: "filter…", description: "Logs tab: placeholder in the search box that filters the log lines by the text typed" })}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="font-mono text-xs"
          />
        </div>
        <Tabs selectedKey={level} onSelectionChange={(key) => setLevel(key as "all" | LogLevel)} variant="secondary" aria-label={intl.formatMessage({ id: "web.routes.tabs.logs.log_level", defaultMessage: "Log level", description: "Logs tab: accessible label of the tabs that filter log lines by level, with an All option; not shown as text" })}>
          <Tabs.List className="w-fit">
            {(["all", ...LEVELS] as const).map((lv) => (
              <TabComponent key={lv} id={lv}>
                {intl.formatMessage(LEVEL_TABS[lv])}{lv !== "all" && intl.formatMessage({ id: "web.routes.tabs.logs.level_count", defaultMessage: " {count}", description: "Logs: number of lines shown after a log level tab label; {count} is that count" }, { count: counts[lv] })}
              </TabComponent>
            ))}
          </Tabs.List>
        </Tabs>
        <span className="text-xs text-muted">{fmt.number(filtered.length)} {intl.formatMessage({ id: "web.routes.tabs.logs.lines", defaultMessage: "lines", description: "Logs tab: word lines after the count shown beside the log; the number is how many lines remain after filtering" })}</span>
        <div className="ml-auto">
          <Button
            size="sm"
            variant="outline"
            onPress={() => {
              window.location.href = Logs.downloadURL(name, ns);
            }}
          >
            <Download className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.tabs.logs.download", defaultMessage: "Download", description: "Logs tab: button that downloads the log as a file; keep it short" })}</Button>
        </div>
      </div>
      <div ref={scrollerRef} className="flex-1 overflow-auto bg-[#0b0b0d] font-mono text-xs">
        {noOutput ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 px-6 py-8 text-center">
            {failed ? (
              <>
                <div className="flex h-12 w-12 items-center justify-center rounded-full bg-danger/10">
                  <AlertTriangle className="h-6 w-6 text-danger" />
                </div>
                <div>
                  <div className="text-sm font-medium text-foreground">
                    {progressMessage ? capitalize(progressMessage) : intl.formatMessage({ id: "web.routes.tabs.logs.server_failed_to_start", defaultMessage: "The server failed to start.", description: "Logs: shown when the server failed to start and gives no reason" })}
                  </div>
                  <div className="mt-1 max-w-md text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.logs.no_container_output_was_captured_check", defaultMessage: "No container output was captured. Check the Overview events for image-pull or scheduling errors.", description: "Logs tab: explanation under the empty log state saying no container output was captured and pointing to the Overview events for pull or scheduling errors" })}</div>
                </div>
              </>
            ) : fileUnavailable ? (
              <>
                <div className="flex h-12 w-12 items-center justify-center rounded-full bg-warning/10">
                  <AlertTriangle className="h-6 w-6 text-warning" />
                </div>
                <div>
                  <div className="text-sm font-medium text-foreground">{intl.formatMessage({ id: "web.routes.tabs.logs.game_log_unavailable", defaultMessage: "Game log unavailable", description: "Logs tab: title of the notice shown when the game log file cannot be read" })}</div>
                  <div className="mt-1 max-w-md text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.logs.couldn_t_reach_the_agent_to_tail_the", defaultMessage: "Couldn't reach the agent to tail the game log file. Container output is still available.", description: "Logs tab: message under the game log unavailable title saying the agent could not be reached and container output is still available" })}</div>
                </div>
                <Button
                  size="sm"
                  variant="primary"
                  onPress={() => setSource("pod")}
                  className="mt-2"
                >{intl.formatMessage({ id: "web.routes.tabs.logs.use_container_output", defaultMessage: "Use container output", description: "Logs tab: button that switches the log view to container output; keep it short" })}</Button>
              </>
            ) : (
              <>
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
                {provisioning ? (
                  <>
                    <div>
                      <div className="text-sm font-medium text-foreground">
                        {progressMessage ? capitalize(progressMessage) : intl.formatMessage({ id: "web.routes.tabs.logs.starting_the_server", defaultMessage: "Starting the server…", description: "Logs: shown while the server is starting and no progress message is available yet" })}
                      </div>
                      <div className="mt-1 max-w-md text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.logs.downloading_game_files_and_starting_up", defaultMessage: "Downloading game files and starting up. The first start can take a few minutes — install output appears here as it streams from the container.", description: "Logs tab: message shown while the server starts for the first time, saying install output streams here from the container" })}</div>
                    </div>
                  </>
                ) : (
                  <div className="text-sm text-muted">
                    {connected
                      ? intl.formatMessage({ id: "web.routes.tabs.logs.connected_waiting_for_output", defaultMessage: "Connected — waiting for output…", description: "Logs: shown once the log stream is connected and no output has arrived yet" })
                      : intl.formatMessage({ id: "web.routes.tabs.logs.connecting_to_log_stream", defaultMessage: "Connecting to the log stream…", description: "Logs: shown while the log stream connection is being opened" })}
                  </div>
                )}
              </>
            )}
          </div>
        ) : (
          <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative", width: "100%" }}>
            {rowVirtualizer.getVirtualItems().map((v) => {
              const row = filtered[v.index];
              return (
                <div
                  key={v.key}
                  style={{
                    position: "absolute", top: 0, left: 0, right: 0,
                    transform: `translateY(${v.start}px)`, height: v.size,
                  }}
                  className={cn("whitespace-pre px-4 leading-[18px]", row.level && LEVEL_CLASS[row.level])}
                >
                  {row.text}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
