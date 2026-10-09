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

type LogComponent = "api" | "operator";

const COMPONENTS: Array<{ value: LogComponent; label: string }> = [
  { value: "api", label: "API server" },
  { value: "operator", label: "Operator" },
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
  const installation = useInstallation();
  if (installation.isPending) return <LoadingCard message="Loading installation…" />;
  if (installation.isError) return <ErrorCard message="Couldn't load installation capabilities." onRetry={() => void installation.refetch()} />;
  if (installation.data.standalone) return <div className="space-y-4 p-6">
    <PageHeader title="System logs" description="Logs for this standalone panel." />
    <Card><CardContent className="space-y-2 p-6">
      <h2 className="font-medium">View logs through your container runtime</h2>
      <p className="text-sm text-muted">For Docker Compose, run <code className="font-mono">docker compose logs --follow</code> from your panel installation directory. Operator logs are available on each workload cluster.</p>
    </CardContent></Card>
  </div>;
  return <ClusterSystemLogs />;
}

function ClusterSystemLogs() {
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
          setError(errorTextWithStatus(err, String(err)));
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
      setError(errorTextWithStatus(err, String(err)));
    }
  };

  return (
    <div className="flex h-full flex-col gap-4 p-6">
      <PageHeader
        title="System logs"
        description="Live logs from the Gameplane control-plane pods."
      />

      <Tabs selectedKey={component} onSelectionChange={(key) => setComponent(key as LogComponent)} variant="secondary" aria-label="Log source">
        <Tabs.List className="w-fit">
          {COMPONENTS.map((c) => (
            <TabComponent key={c.value} id={c.value}>{c.label}</TabComponent>
          ))}
        </Tabs.List>
      </Tabs>

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2 text-xs text-muted">
          Tail
          <Select
            aria-label="Tail lines"
            selectedKey={String(tail)}
            onSelectionChange={(key) => setTail(Number(key))}
          >
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox aria-label="Tail lines">
                {TAIL_OPTIONS.map((n) => (
                  <ListBoxItem key={n} id={String(n)}>
                    {n} lines
                  </ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>

        <div className="flex items-center gap-2 text-xs text-muted">
          Follow
          <Switch isSelected={follow} onChange={setFollow} aria-label="Follow">
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
        </div>

        <span
          className="inline-flex h-6 items-center rounded bg-primary/10 px-2 font-mono text-xs text-primary"
          title="Pod currently streamed"
        >
          {pod || "—"}
        </span>

        <div className="ml-auto">
          <Button variant="outline" onPress={() => void downloadLogs()}>
            <Download className="h-4 w-4" /> Download
          </Button>
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
            {error ? "No output." : "Waiting for output…"}
          </div>
        )}
      </div>
    </div>
  );
}
