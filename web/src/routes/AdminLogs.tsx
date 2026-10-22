import { useCallback, useEffect, useRef, useState } from "react";
import { Download } from "lucide-react";
import { Select, Switch, ListBox, ListBoxItem, Tabs, Tab as TabComponent, Button, Card, CardContent } from "@heroui/react";
import { APIError } from "@/lib/api";
import { errorTextWithStatus } from "@/lib/errors";
import { withCluster } from "@/lib/endpoints";
import { PageHeader } from "@/components/ui/PageHeader";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { useInstallation } from "@/lib/useInstallation";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

type LogComponent = "api" | "operator";

const COMPONENT_LABELS = defineMessages({
  api: { id: "web.routes.admin_logs.component_api", defaultMessage: "API server", description: "Admin logs: tab label for the API server log source" },
  operator: { id: "web.routes.admin_logs.component_operator", defaultMessage: "Operator", description: "Admin logs: tab label for the operator log source" },
});

const COMPONENTS: Array<{ value: LogComponent; label: MessageDescriptor }> = [
  { value: "api", label: COMPONENT_LABELS.api },
  { value: "operator", label: COMPONENT_LABELS.operator },
];

const TAIL_OPTIONS = [100, 500, 1000, 5000] as const;

// The scrollback buffer is capped so a chatty follow stream can't grow the
// tab's memory without bound; the head is trimmed at a line boundary.
const MAX_BUFFER = 2 * 1024 * 1024;
const RECONNECT_DELAY_MS = 1_000;

// capBuffer keeps at most `max` characters of scrollback, dropping the
// oldest lines first. Exported for tests.
export function capBuffer(s: string, max: number = MAX_BUFFER): string {
  if (s.length <= max) return s;
  const cut = s.slice(s.length - max);
  const nl = cut.indexOf("\n");
  return nl >= 0 ? cut.slice(nl + 1) : cut;
}

function logsURL(
  component: LogComponent,
  tailLines: number,
  follow: boolean,
  pod?: string,
): string {
  const params = new URLSearchParams({
    tailLines: String(tailLines),
    follow: String(follow),
  });
  if (pod) params.set("pod", pod);
  return `/admin/system-logs/${component}?${params.toString()}`;
}

// streamSystemLogs fetches one plaintext log stream and forwards decoded
// chunks as they arrive. The JSON wrapper in lib/api.ts can't consume a
// text stream, so this follows its conventions (relative URL, cookie
// credentials, APIError on non-2xx) with a ReadableStream reader instead.
async function streamSystemLogs(opts: {
  url: string;
  signal: AbortSignal;
  onPod: (pod: string) => void;
  onChunk: (text: string) => void;
}): Promise<void> {
  const res = await fetch(opts.url, {
    credentials: "include",
    signal: opts.signal,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new APIError(res.status, text);
  }
  const pod = res.headers.get("X-Gameplane-Pod");
  if (pod) opts.onPod(pod);
  if (!res.body) return;
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    opts.onChunk(decoder.decode(value, { stream: true }));
  }
  const rest = decoder.decode();
  if (rest) opts.onChunk(rest);
}

export function AdminLogsPage() {
  const intl = useOptionalIntl();
  const installation = useInstallation();
  if (installation.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.routes.admin_logs.loading_installation", defaultMessage: "Loading installation…", description: "Admin logs: loading text shown in place of the page while installation details are fetched" })} />;
  if (installation.isError) return <ErrorCard message={intl.formatMessage({ id: "web.routes.admin_logs.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Admin logs: error message shown when installation details fail to load; a retry button follows it" })} onRetry={() => void installation.refetch()} />;
  if (installation.data.standalone) return <div className="space-y-4 p-6">
    <PageHeader title={intl.formatMessage({ id: "web.routes.admin_logs.system_logs", defaultMessage: "System logs", description: "Admin logs: page title at the top of the system logs screen; keep it short" })} description={intl.formatMessage({ id: "web.routes.admin_logs.logs_for_this_standalone_panel", defaultMessage: "Logs for this standalone panel.", description: "Admin logs: description under the title in the standalone panel view, saying these logs belong to this panel only" })} />
    <Card><CardContent className="space-y-2 p-6">
      <h2 className="font-medium">{intl.formatMessage({ id: "web.routes.admin_logs.view_logs_through_your_container_runtime", defaultMessage: "View logs through your container runtime", description: "Admin logs: heading in the standalone panel view that introduces how to read logs through the container runtime" })}</h2>
      <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_logs.for_docker_compose_run", defaultMessage: "For Docker Compose, run", description: "Admin logs: opening words of the standalone hint paragraph, followed by a command in monospace and the rest of the sentence" })} <code className="font-mono">{intl.formatMessage({ id: "web.routes.admin_logs.docker_compose_logs_follow", defaultMessage: "docker compose logs --follow", description: "Admin logs: shell command shown in monospace inside the standalone hint, the Docker Compose command to stream logs; technical term, usually left untranslated" })}</code> {intl.formatMessage({ id: "web.routes.admin_logs.from_your_panel_installation_directory", defaultMessage: "from your panel installation directory. Operator logs are available on each workload cluster.", description: "Admin logs: end of the standalone hint, asking to run the command from the install directory; also notes operator logs are on each workload cluster" })}</p>
    </CardContent></Card>
  </div>;
  return <ClusterSystemLogs />;
}

function ClusterSystemLogs() {
  const intl = useOptionalIntl();
  const [component, setComponent] = useState<LogComponent>("api");
  const [tail, setTail] = useState(500);
  const [follow, setFollow] = useState(false);
  const [pod, setPod] = useState("");
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  // Sticks the panel to the bottom on new output; a manual scroll up
  // releases it, scrolling back down re-engages it.
  const stickRef = useRef(true);

  useEffect(() => {
    const ctrl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    // Pin follow reconnects to the pod the first response picked, so the
    // stream doesn't hop between replicas; a component/tail change resets it.
    let currentPod = "";
    stickRef.current = true;

    const run = async () => {
      // Clear stale display state as the first step of a fresh streaming
      // session (a new component/tail/follow combination).
      setText("");
      setPod("");
      setError("");
      for (;;) {
        try {
          await streamSystemLogs({
            url: withCluster(logsURL(component, tail, follow, currentPod || undefined)),
            signal: ctrl.signal,
            onPod: (p) => {
              currentPod = p;
              setPod(p);
            },
            onChunk: (chunk) => setText((prev) => capBuffer(prev + chunk)),
          });
        } catch (err) {
          if (ctrl.signal.aborted) return;
          setError(errorTextWithStatus(err, String(err), intl));
          return; // only clean stream ends reconnect; errors stop the loop
        }
        // A non-follow fetch is one-shot. The server caps follow streams at
        // ~50s, so a clean end while following just means "reconnect".
        if (!follow || ctrl.signal.aborted) return;
        // Abortable sleep: abort must also resolve the promise, or this
        // async frame stays suspended forever after an unmount/param change.
        await new Promise<void>((resolve) => {
          timer = setTimeout(resolve, RECONNECT_DELAY_MS);
          ctrl.signal.addEventListener(
            "abort",
            () => {
              clearTimeout(timer);
              resolve();
            },
            { once: true },
          );
        });
        if (ctrl.signal.aborted) return;
      }
    };
    void run();
    return () => ctrl.abort();
  }, [component, tail, follow]);

  // Auto-scroll to the newest output unless the user scrolled up to read.
  useEffect(() => {
    const el = scrollerRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  }, [text]);

  const onScroll = useCallback(() => {
    const el = scrollerRef.current;
    if (!el) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }, []);

  const downloadLogs = async () => {
    setError("");
    try {
      const res = await fetch(withCluster(logsURL(component, tail, false, pod || undefined)), {
        credentials: "include",
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new APIError(res.status, body);
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `gameplane-${component}-${new Date().toISOString().replace(/[:.]/g, "-")}.log`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(errorTextWithStatus(err, String(err), intl));
    }
  };

  return (
    <div className="flex h-full flex-col gap-4 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.admin_logs.system_logs", defaultMessage: "System logs", description: "Admin logs: page title at the top of the system logs screen; keep it short" })}
        description={intl.formatMessage({ id: "web.routes.admin_logs.live_logs_from_the_gameplane_control", defaultMessage: "Live logs from the Gameplane control-plane pods.", description: "Admin logs: description under the title of the cluster logs view, saying the lines come from the Gameplane control-plane pods" })}
      />

      <Tabs selectedKey={component} onSelectionChange={(key) => setComponent(key as LogComponent)} variant="secondary" aria-label={intl.formatMessage({ id: "web.routes.admin_logs.log_source", defaultMessage: "Log source", description: "Admin logs: accessible name of the tab group for choosing which component's logs to show" })}>
        <Tabs.List className="w-fit">
          {COMPONENTS.map((c) => (
            <TabComponent key={c.value} id={c.value}>{intl.formatMessage(c.label)}</TabComponent>
          ))}
        </Tabs.List>
      </Tabs>

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_logs.tail", defaultMessage: "Tail", description: "Admin logs: label in front of the line-count dropdown in the log toolbar; keep it short" })}<Select
            aria-label={intl.formatMessage({ id: "web.routes.admin_logs.tail_lines", defaultMessage: "Tail lines", description: "Admin logs: accessible name of the dropdown that sets how many recent lines to load" })}
            selectedKey={String(tail)}
            onSelectionChange={(key) => setTail(Number(key))}
          >
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox aria-label={intl.formatMessage({ id: "web.routes.admin_logs.tail_lines", defaultMessage: "Tail lines", description: "Admin logs: accessible name of the dropdown that sets how many recent lines to load" })}>
                {TAIL_OPTIONS.map((n) => (
                  <ListBoxItem key={n} id={String(n)}>{intl.formatMessage({ id: "web.routes.admin_logs.n_lines", defaultMessage: "{n} lines", description: "Admin logs: sentence with n in ListBoxItem" }, { n: n })}</ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>

        <div className="flex items-center gap-2 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_logs.follow", defaultMessage: "Follow", description: "Admin logs: label of the switch that keeps the log view following new lines as they arrive; keep it short" })}<Switch isSelected={follow} onChange={setFollow} aria-label={intl.formatMessage({ id: "web.routes.admin_logs.follow", defaultMessage: "Follow", description: "Admin logs: label of the switch that keeps the log view following new lines as they arrive; keep it short" })}>
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
        </div>

        <span
          className="inline-flex h-6 items-center rounded bg-primary/10 px-2 font-mono text-xs text-primary"
          title={intl.formatMessage({ id: "web.routes.admin_logs.pod_currently_streamed", defaultMessage: "Pod currently streamed", description: "Admin logs: badge showing the pod whose logs are being streamed right now; keep it short" })}
        >
          {pod || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
        </span>

        <div className="ml-auto">
          <Button variant="outline" onPress={() => void downloadLogs()}>
            <Download className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.admin_logs.download", defaultMessage: "Download", description: "Admin logs: button that downloads the logs shown on screen as a file; keep it short" })}</Button>
        </div>
      </div>

      {error && <div className="text-xs text-danger">{error}</div>}

      <div
        ref={scrollerRef}
        onScroll={onScroll}
        data-testid="log-scroller"
        className="min-h-0 flex-1 overflow-auto rounded-md border border-border bg-[#0b0b0d] font-mono text-xs scrollbar-thin"
      >
        {text ? (
          <div className="px-4 py-3">
            {text.split("\n").map((line, idx) => {
              if (!line) return null;
              let timestamp = "";
              let level = "";
              let message = line;
              let parsed: Record<string, unknown> = {};

              try {
                parsed = JSON.parse(line);
                if (parsed.ts) timestamp = String(parsed.ts);
                else if (parsed.time) timestamp = String(parsed.time);
                if (parsed.level) level = String(parsed.level);
                if (parsed.msg) message = String(parsed.msg);
              } catch {
                // Not JSON, use raw line
              }

              const lvl = String(level ?? "").toUpperCase();
              const levelColor =
                lvl === "WARN" ? "text-[var(--warning)]" :
                lvl === "ERROR" ? "text-[var(--danger)]" :
                "text-foreground";

              // Collect structured fields (all keys except ts, time, level, msg)
              const structuredFields = Object.entries(parsed).filter(
                ([key]) => !["ts", "time", "level", "msg"].includes(key),
              );

              return (
                <div key={idx} className="flex gap-3 leading-[18px]">
                  {timestamp && (
                    <span className="shrink-0 text-muted">{timestamp}</span>
                  )}
                  <span className={`break-words ${levelColor}`}>
                    {message}
                  </span>
                  {structuredFields.length > 0 && (
                    <span className="text-xs text-muted/60">
                      {structuredFields.map(([key, val]) => `${key}=${val}`).join(" ")}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        ) : (
          <div className="flex h-full items-center justify-center text-muted">
            {error
              ? intl.formatMessage({ id: "web.routes.admin_logs.no_output", defaultMessage: "No output.", description: "Admin logs: shown in the log panel when the stream failed" })
              : intl.formatMessage({ id: "web.routes.admin_logs.waiting_for_output", defaultMessage: "Waiting for output…", description: "Admin logs: shown in the log panel while no output has arrived yet" })}
          </div>
        )}
      </div>
    </div>
  );
}

// Scenario 8 check.
