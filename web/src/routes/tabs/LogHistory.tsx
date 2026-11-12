import { useEffect, useRef, useState } from "react";
import { Button } from "@heroui/react";
import { defineMessages } from "react-intl";

import { APIError } from "@/lib/api";
import type { LogHistoryPage } from "@/lib/endpoints";
import { useOptionalIntl } from "@/lib/i18n/format";
import { useResourceClient, useResourceTarget } from "@/lib/resourceTarget";

const labels = defineMessages({
  open: { id: "web.logs.history.open", defaultMessage: "Earlier output", description: "Open a snapshot of older log output" },
  older: { id: "web.logs.history.older", defaultMessage: "Load older", description: "Read the preceding page of this log" },
  hide: { id: "web.logs.history.hide", defaultMessage: "Hide history", description: "Close the historical log page" },
  loading: { id: "web.logs.history.loading", defaultMessage: "Loading history…", description: "A log history page is loading" },
  empty: { id: "web.logs.history.empty", defaultMessage: "No earlier output.", description: "The log snapshot is empty" },
  limited: { id: "web.logs.history.limited", defaultMessage: "Some output exceeds the history window.", description: "A container record or history read exceeded its size limit" },
  changed: { id: "web.logs.history.changed", defaultMessage: "This log changed or its earlier output expired. Reopen history to read the current log.", description: "A server log cursor no longer matches its source" },
  failed: { id: "web.logs.history.failed", defaultMessage: "Could not load log history.", description: "The selected server's log history request failed" },
  console: { id: "web.logs.history.console", defaultMessage: "Recent pod output", description: "Pod log snapshot shown alongside the live terminal" },
  boundary: { id: "web.logs.history.boundary", defaultMessage: "Pod logs and the live console use independent streams. Output may overlap or have gaps between them.", description: "Explain the boundary between the console and the pod output snapshot" },
});

type Props = { name: string; ns?: string; source: "pod" | "file"; console?: boolean };

export function LogHistory(props: Props) {
  const target = useResourceTarget({ name: props.name, namespace: props.ns });
  return <History key={JSON.stringify([target, props.source])} {...props} />;
}

function History({ name, ns, source, console: consoleView = false }: Props) {
  const intl = useOptionalIntl();
  const target = useResourceTarget({ name, namespace: ns });
  const client = useResourceClient(target);
  const [open, setOpen] = useState(consoleView);
  const [before, setBefore] = useState("");
  const [page, setPage] = useState<LogHistoryPage>();
  const [error, setError] = useState<"changed" | "failed">();
  const [loading, setLoading] = useState(consoleView);
  const output = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void client.withSignal(controller.signal).Logs.history(name, source, before, ns).then((result) => {
      if (controller.signal.aborted) return;
      setPage(result);
      if (output.current) output.current.scrollTop = 0;
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setError(cause instanceof APIError && cause.status === 409 ? "changed" : "failed");
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [open, before, name, ns, source, client]);

  const toggle = () => {
    setPage(undefined);
    setError(undefined);
    setBefore("");
    setLoading(!open);
    setOpen(!open);
  };
  return <section className="shrink-0 border-b border-border px-3 py-1.5">
    <div className="flex flex-wrap items-center gap-2">
      {consoleView && <span className="text-xs font-medium">{intl.formatMessage(labels.console)}</span>}
      <Button size="sm" variant="ghost" aria-expanded={open} onPress={toggle}>
        {intl.formatMessage(open ? labels.hide : labels.open)}
      </Button>
      {open && page?.before && !error && <Button size="sm" variant="outline" isDisabled={loading} onPress={() => {
        setLoading(true);
        setBefore(page.before ?? "");
      }}>
        {intl.formatMessage(labels.older)}
      </Button>}
      {open && page?.source && <span className="font-mono text-xs text-muted">{page.source}</span>}
    </div>
    {open && <div className="space-y-1 text-xs">
      {consoleView && <p className="text-muted">{intl.formatMessage(labels.boundary)}</p>}
      <div role="status">{loading ? intl.formatMessage(labels.loading) : error ? intl.formatMessage(labels[error]) : page?.limited ? intl.formatMessage(labels.limited) : null}</div>
      {page?.text ? <pre ref={output} tabIndex={0} className="max-h-40 overflow-auto whitespace-pre-wrap break-all bg-default p-2">{page.text}</pre> :
        !loading && !error && !page?.limited && <p>{intl.formatMessage(labels.empty)}</p>}
    </div>}
  </section>;
}
