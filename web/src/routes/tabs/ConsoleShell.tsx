import { useRef, useState, type KeyboardEvent } from "react";
import { Button, Input } from "@heroui/react";
import { Download, Eraser, Maximize2 } from "lucide-react";

import { cn } from "@/lib/utils";
import type { WSStatus } from "@/lib/ws";
import type { ConsoleHandle } from "./useConsoleTerminal";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

// Connection indicator styling, keyed exhaustively on WSStatus so a new
// state can't render styleless. "connecting" and "reconnecting" share the
// pulsing amber dot; "open" is the steady green ● LIVE.
const STATUS_DOT: Record<WSStatus, string> = {
  open: "bg-success",
  reconnecting: "bg-warning animate-pulse",
  connecting: "bg-warning animate-pulse",
  closed: "bg-muted",
};
const STATUS_LABEL: Record<WSStatus, MessageDescriptor> = defineMessages({
  open: { id: "web.routes.tabs.console.status_live", defaultMessage: "LIVE", description: "Console: connection status badge when the console stream is connected" },
  reconnecting: { id: "web.routes.tabs.console.status_reconnecting", defaultMessage: "reconnecting…", description: "Console: connection status badge while the console stream reconnects" },
  connecting: { id: "web.routes.tabs.console.status_connecting", defaultMessage: "connecting…", description: "Console: connection status badge while the console stream connects" },
  closed: { id: "web.routes.tabs.console.status_offline", defaultMessage: "offline", description: "Console: connection status badge when the console stream is closed" },
});

// Commands kept for Up/Down recall in the input bar; per mount, not persisted.
const HISTORY_LIMIT = 100;

// ConsoleShell is the M8 chrome from design.pen frame Xn5ns: a header
// toolbar (connection indicator + Clear/Download/Fullscreen) bracketing the
// xterm host, and a dedicated command-input bar. All behavior comes from the
// useConsoleTerminal handle; this component is presentation + local input.
export function ConsoleShell({ handle }: { handle: ConsoleHandle }) {
  const intl = useOptionalIntl();
  const { hostRef, status, clear, download, toggleFullscreen, sendCommand } = handle;
  const [cmd, setCmd] = useState("");
  // history holds sent commands, oldest first. histIdx is the entry being
  // shown while browsing (null = editing a fresh line), and draft keeps the
  // unsent line so ArrowDown past the newest entry restores it.
  const history = useRef<string[]>([]);
  const histIdx = useRef<number | null>(null);
  const draft = useRef("");
  const onHistoryKey = (e: KeyboardEvent<HTMLInputElement>) => {
    const h = history.current;
    if (e.key === "ArrowUp") {
      if (h.length === 0) return;
      e.preventDefault();
      if (histIdx.current === null) {
        draft.current = cmd;
        histIdx.current = h.length - 1;
      } else if (histIdx.current > 0) {
        histIdx.current -= 1;
      }
      setCmd(h[histIdx.current]);
    } else if (e.key === "ArrowDown") {
      if (histIdx.current === null) return;
      e.preventDefault();
      if (histIdx.current < h.length - 1) {
        histIdx.current += 1;
        setCmd(h[histIdx.current]);
      } else {
        histIdx.current = null;
        setCmd(draft.current);
      }
    }
  };
  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <span className="inline-flex items-center gap-1.5 font-mono text-xs">
          <span className={cn("h-2 w-2 rounded-full", STATUS_DOT[status])} aria-hidden />
          {intl.formatMessage(STATUS_LABEL[status])}
        </span>
        <div className="ml-auto flex flex-wrap gap-2">
          <Button
            isIconOnly
            variant="ghost"
            size="sm"
            onPress={clear}
            aria-label={intl.formatMessage({ id: "web.routes.tabs.console_shell.clear_terminal", defaultMessage: "Clear terminal", description: "Console shell: icon-only button that clears the console output; read by screen readers; keep it short" })}
          >
            <Eraser className="h-4 w-4" />
          </Button>
          <Button
            isIconOnly
            variant="ghost"
            size="sm"
            onPress={download}
            aria-label={intl.formatMessage({ id: "web.routes.tabs.console_shell.download_terminal_buffer", defaultMessage: "Download terminal buffer", description: "Console shell: icon-only button that downloads the console output as a file; read by screen readers; keep it short" })}
          >
            <Download className="h-4 w-4" />
          </Button>
          <Button
            isIconOnly
            variant="ghost"
            size="sm"
            onPress={toggleFullscreen}
            aria-label={intl.formatMessage({ id: "web.routes.tabs.console_shell.toggle_fullscreen", defaultMessage: "Toggle fullscreen", description: "Console shell: icon-only button that switches the console between normal and full-screen view; read by screen readers; keep it short" })}
          >
            <Maximize2 className="h-4 w-4" />
          </Button>
        </div>
      </div>
      <div className="flex-1 min-h-0 p-4">
        <div ref={hostRef} className="h-full rounded-lg border border-border bg-[#0b0b0d] p-2" />
      </div>
      <form
        className="flex items-center gap-2 border-t border-border px-4 py-2"
        onSubmit={(e) => {
          e.preventDefault();
          const line = cmd.trim();
          if (!line) return;
          sendCommand(line);
          const h = history.current;
          if (h[h.length - 1] !== line) h.push(line);
          if (h.length > HISTORY_LIMIT) h.shift();
          histIdx.current = null;
          draft.current = "";
          setCmd("");
        }}
      >
        <Input
          placeholder={intl.formatMessage({ id: "web.routes.tabs.console_shell.type_a_command_recalls_history", defaultMessage: "Type a command… (↑/↓ recalls history)", description: "Console shell: placeholder text in the command box, shown before typing; the arrow keys recall earlier commands" })}
          value={cmd}
          onChange={(e) => setCmd(e.target.value)}
          onKeyDown={onHistoryKey}
          className="flex-1 font-mono text-xs"
        />
        <Button
          type="submit"
          size="sm"
          variant="primary"
          isDisabled={!cmd.trim()}
        >{intl.formatMessage({ id: "web.routes.tabs.console_shell.send", defaultMessage: "Send", description: "Console shell: button that sends the typed command to the server; disabled while the command box is empty; keep it short" })}</Button>
      </form>
    </div>
  );
}
